package udpsender

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

type RecvService struct {
	key          string
	ch           chan *RecvBlock
	transactions map[string]*RecvTransaction
	sock         *net.UDPConn
	fn           RecvHandleFunc
}

type RecvTransaction struct {
	num         int
	blocks      map[int]*RecvBlock
	lastTime    int64
	blockSize   int64
	Speed       float64
	completed   bool
	Addr        *net.UDPAddr
	Transaction []byte
}

type RecvBlock struct {
	transaction      []byte
	blockHash        []byte
	fileHash         []byte
	fileData         []byte
	num, id          int32
	start, end, size int64
	time1            int64
	addr             *net.UDPAddr
}

type RecvHandleFunc func(service *RecvService, transaction *RecvTransaction)

func InitRecvService(key string, url string, fn RecvHandleFunc) (service *RecvService, err error) {
	service = new(RecvService)
	service.key = key
	service.fn = fn
	service.ch = make(chan *RecvBlock, 100)
	service.transactions = map[string]*RecvTransaction{}

	addr, err := net.ResolveUDPAddr("udp", url)
	if err != nil {
		return nil, err
	}
	service.sock, err = net.ListenUDP("udp", addr)
	if err != nil {
		return nil, err
	}
	go service.loop()

	return service, nil
}

func InitRecvTransaction(block *RecvBlock) *RecvTransaction {
	transaction := new(RecvTransaction)
	transaction.blocks = map[int]*RecvBlock{}
	transaction.lastTime = time.Now().UnixNano()
	transaction.blockSize = int64(len(block.fileData))
	transaction.num = int(block.num)
	transaction.Addr = block.addr
	transaction.Transaction = block.transaction
	return transaction
}
func (service *RecvService) processRecvBlock(block *RecvBlock) {
	stransaction := hex.EncodeToString(block.transaction)
	transaction, ok := service.transactions[stransaction]
	if !ok {
		fmt.Println("Creating transaction:", stransaction)
		transaction = InitRecvTransaction(block)
		service.transactions[stransaction] = transaction
	}
	if transaction.num != int(block.num) {
		// error
	}
	block.time1 = time.Now().UnixNano()
	fdtime := float64(block.time1 - transaction.lastTime)
	transaction.lastTime = block.time1
	speed := float64(len(block.fileData)) / fdtime * float64(time.Second)
	transaction.Speed = movingExpAvg(speed, transaction.Speed, fdtime, float64(time.Second))

	_, ok = transaction.blocks[int(block.id)]
	if ok {
		// dupe
		return
	}
	transaction.blocks[int(block.id)] = block
	// fmt.Printf("got %d %d %d\n%s\n", block.id, len(transaction.blocks), transaction.num, string(block.fileData))
	fmt.Printf("got %d %d %d %f\n", block.id, len(transaction.blocks), transaction.num, transaction.Speed)
	if len(transaction.blocks) == transaction.num {
		transaction.completed = true
		service.fn(service, transaction)
		// fmt.Printf("Finish RecvTransaction %f\n", transaction.Speed)
		// // finish transaction
		// // save file

		// f1, err := os.Create("incomming/" + stransaction + "/file.out")
		// if err != nil {
		// 	panic(err)
		// }
		// // defer f1.Close()

		// for i := 1; i <= transaction.num; i++ {
		// 	block1, ok := transaction.blocks[i]
		// 	if !ok {
		// 		// error
		// 		panic(errors.New(fmt.Sprintf("Not all blocks %d", i)))
		// 	}
		// 	f1.Write(block1.fileData)
		// }
		// f1.Close()
		// file recieved
	}
}
func (transaction *RecvTransaction) WriteTo(w io.Writer) error {
	for i := 1; i <= transaction.num; i++ {
		block1, ok := transaction.blocks[i]
		if !ok {
			// error
			return errors.New(fmt.Sprintf("Not all blocks %d", i))
		}
		w.Write(block1.fileData)
	}
	return nil
}
func (service *RecvService) Loop() {

	for {
		// len, remote, err := sock.ReadFromUDP(buf[:])
		var buf [1 << 16]byte
		len, remote, err := service.sock.ReadFromUDP(buf[:])
		if err != nil {
			panic(err)
		}
		service.processPacket(remote, buf[:len])
	}
}
func (service *RecvService) loop() {
	ticker := time.Tick(100 * time.Millisecond)
	for {

		select {
		case blck, ok := <-service.ch:
			if ok {
				service.processRecvBlock(blck)
			}
		case <-ticker:
			time1 := time.Now().UnixNano()
			for _, transaction := range service.transactions {
				if transaction.completed {
					continue
				}

				fdtime := float64(time1 - transaction.lastTime)
				transaction.lastTime = time1
				transaction.Speed = movingExpAvg(0.0, transaction.Speed, fdtime, float64(time.Second))

				//
				// find last_block
				last_block := -1
				var block *RecvBlock
				for i, _block := range transaction.blocks {
					if last_block < i {
						last_block = i
						block = _block
					}
				}
				if last_block == -1 {
					continue
				}
				// dtime  = Now - last_block.time
				dtime := time1 - block.time1
				// last_block + (dtime-100ms)*speed/blocksize
				shift := last_block + int((float64(dtime)-100.0*float64(time.Millisecond))*transaction.Speed/float64(transaction.blockSize))
				// get first 100 un recieved blocks

				req := []int{}

				for i := 1; i <= transaction.num; i++ {
					if i > shift {
						break
					}
					_, ok := transaction.blocks[i]
					if !ok {
						req = append(req, i)
						if len(req) >= 100 {
							break
						}
					}
				}

				if len(req) > 0 {
					service.rerequestPackets(transaction, req)
				}
			}
		}

	}

}
func (service *RecvService) rerequestPackets(transaction *RecvTransaction, req []int) {
	r1 := make([]byte, 16)
	_, err := rand.Read(r1)
	if err != nil {
		panic(err)
	}

	data := append(transaction.Transaction, r1...)

	buffer := new(bytes.Buffer)
	binary.Write(buffer, binary.LittleEndian, int32(1))
	binary.Write(buffer, binary.LittleEndian, int32(len(req)))
	for _, i := range req {
		binary.Write(buffer, binary.LittleEndian, int32(i))
	}

	block, err := aes.NewCipher([]byte(service.key))
	if err != nil {
		panic(err)
	}

	data1 := buffer.Bytes()

	stream := cipher.NewCFBEncrypter(block, r1)
	stream.XORKeyStream(data1, data1)

	data = append(data, data1...)

	service.sock.WriteToUDP(data, transaction.Addr)
}
func (service *RecvService) processPacket(addr *net.UDPAddr, buf []byte) {
	blck := new(RecvBlock)
	blck.transaction = buf[:16]
	blck.addr = addr
	r1 := buf[16:32]
	data2 := buf[32:]

	block, err := aes.NewCipher([]byte(service.key))
	if err != nil {
		panic(err)
	}

	stream := cipher.NewCFBDecrypter(block, r1)

	stream.XORKeyStream(data2, data2)

	// fileHash := data2[0:20]

	buffer := bytes.NewBuffer(data2[20:52])
	binary.Read(buffer, binary.LittleEndian, &blck.id)
	binary.Read(buffer, binary.LittleEndian, &blck.num)
	binary.Read(buffer, binary.LittleEndian, &blck.start)
	binary.Read(buffer, binary.LittleEndian, &blck.end)
	binary.Read(buffer, binary.LittleEndian, &blck.size)
	blck.blockHash = data2[52:72]
	bufCopy := make([]byte, len(data2[72:]))
	copy(bufCopy[:], data2[72:])
	blck.fileData = bufCopy
	blockHash := sha1.Sum(blck.fileData)
	verified := bytes.Equal(blck.blockHash, blockHash[:])

	fmt.Println("processPacker:", blck.id, hex.EncodeToString(r1))

	if verified {
		// os.MkdirAll("incomming/"+hex.EncodeToString(blck.transaction), 0700)
		// f1, err := os.Create("incomming/" + hex.EncodeToString(blck.transaction) + "/" + fmt.Sprintf("%d.data", blck.id))
		// if err != nil {
		// 	panic(err)
		// }
		// defer f1.Close()
		// f1.Write(blck.fileData)
		service.ch <- blck
	} else {
		fmt.Println("wrong hash %#v %#v", blck.blockHash, blockHash[:])
	}

}

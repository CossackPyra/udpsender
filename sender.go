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
	"net"
	"os"
	"time"
)

type SendService struct {
	conn        *net.UDPConn
	addr        *net.UDPAddr
	filename    string
	blockSize   int64
	size        int64
	num         int64
	reminder    int64
	key         string
	transaction []byte
	fileHash    []byte
	f1          *os.File
	speed       int64
}

func InitSendService(speed int64, from string, to string) (service *SendService, err error) {
	service = new(SendService)
	service.speed = speed

	addr, err := net.ResolveUDPAddr("udp4", from)
	if err != nil {
		return nil, err
	}
	service.addr, err = net.ResolveUDPAddr("udp4", to)
	if err != nil {
		return nil, err
	}
	service.conn, err = net.ListenUDP("udp4", addr)
	if err != nil {
		return nil, err
	}

	// conn, err := net.Dial("udp", "127.0.0.1:12345")

	// if err != nil {
	// 	panic(err)
	// }

	// service.conn = conn

	return service, nil
}
func (service *SendService) SendFile(key string, filename string) {
	fmt.Println("SendFile")
	service.filename = filename
	service.key = key

	service.transaction = make([]byte, 16)
	_, err := rand.Read(service.transaction)
	if err != nil {
		panic(err)
	}

	service.blockSize = 29999
	// service.blockSize = 10

	fi, err := os.Stat(filename)
	if err != nil {
		panic(err)
	}
	service.size = fi.Size()
	service.num = service.size / service.blockSize
	service.reminder = service.size % service.blockSize

	service.f1, err = os.Open(filename)
	if err != nil {
		panic(err)
	}
	//defer f1.Close()

	h := sha1.New()

	buf := make([]byte, 1<<16)
	for {
		n, err := service.f1.Read(buf)

		if err != nil {
			// panic(err)
			// EOF usually
			break
		}
		if n == 0 {
			break
		}
		h.Write(buf[:n])
	}

	service.fileHash = h.Sum(nil)

	fmt.Println("Transaction: " + hex.EncodeToString(service.transaction))
	fmt.Println("File hash: " + hex.EncodeToString(service.fileHash))
	fmt.Printf("num: %d reminder: %d\n", service.num, service.reminder)
	// send info block
	// send blocks
	num := service.num
	if service.reminder > 0 {
		service.num++
	}
	time1 := time.Now().UnixNano()

	for i := int64(1); i <= num; i++ {
		service.sendBlock1(int(i)) //, service.blockSize*(i-1), service.blockSize*i)
		time2 := time.Now().UnixNano()
		dt1 := time2 - time1
		time1 = time2
		dt2 := int64(time.Second) * service.blockSize / service.speed
		dt3 := dt2 - dt1
		if dt3 > 0 {
			time.Sleep(time.Duration(dt3))
		}

		// time.Sleep(10 * time.Millisecond)
	}
	// send reminder
	if service.reminder > 0 {
		service.sendBlock1(int(num + 1)) //, service.blockSize*num, service.blockSize*num+reminder)
	}

	for {
		// len, remote, err := sock.ReadFromUDP(buf[:])
		var buf [1 << 16]byte
		n, _, err := service.conn.ReadFromUDP(buf[:])
		if err != nil {
			panic(err)
		}

		//go service.loop()
		service.processPacket(buf[:n])

	}

}
func (service *SendService) processPacket(buf []byte) {
	// transaction := buf[:16]
	r1 := buf[16:32]

	data2 := buf[32:]

	block, err := aes.NewCipher([]byte(service.key))
	if err != nil {
		panic(err)
	}

	stream := cipher.NewCFBDecrypter(block, r1)

	stream.XORKeyStream(data2, data2)

	buffer := bytes.NewBuffer(data2)

	var type1 int32

	for {
		err = binary.Read(buffer, binary.LittleEndian, &type1)
		if err != nil {
			break
		}
		if type1 == 1 {
			var num int32
			err = binary.Read(buffer, binary.LittleEndian, &num)
			if err != nil {
				break
			}
			for {
				var id int32
				err = binary.Read(buffer, binary.LittleEndian, &id)
				if err != nil {
					break
				}
				fmt.Printf("Extra Resend %d\n", id)
				service.sendBlock1(int(id))
			}

		}
	}

}
func (service *SendService) sendBlock1(id int) {
	if service.reminder == 0 {
		service.sendBlock(id, service.blockSize*int64(id-1), service.blockSize*int64(id))
	} else {
		if int64(id) == service.num {
			service.sendBlock(id, service.blockSize*int64(id-1), service.blockSize*int64(id-1)+service.reminder)
		} else {
			service.sendBlock(id, service.blockSize*int64(id-1), service.blockSize*int64(id))
		}
	}

}
func (service *SendService) sendBlock(id int, start int64, end int64) {
	r1 := make([]byte, 16)
	_, err := rand.Read(r1)
	if err != nil {
		panic(err)
	}

	service.f1.Seek(start, 0)
	fileData := make([]byte, end-start)
	n, err := service.f1.Read(fileData)
	if err != nil {
		panic(err)
	}
	if n != len(fileData) {
		panic(errors.New(fmt.Sprintf("failed to read all bytes %d %d %d", id, n, len(fileData))))
	}
	blockHash := sha1.Sum(fileData)

	data := append(service.transaction, r1...)

	buffer := new(bytes.Buffer)

	buffer.Write(service.fileHash)
	binary.Write(buffer, binary.LittleEndian, int32(id))
	binary.Write(buffer, binary.LittleEndian, int32(service.num))
	binary.Write(buffer, binary.LittleEndian, start)
	binary.Write(buffer, binary.LittleEndian, end)
	binary.Write(buffer, binary.LittleEndian, service.size)
	buffer.Write(blockHash[:])
	buffer.Write(fileData)

	block, err := aes.NewCipher([]byte(service.key))
	if err != nil {
		panic(err)
	}

	data1 := buffer.Bytes()

	stream := cipher.NewCFBEncrypter(block, r1)
	stream.XORKeyStream(data1, data1)

	data = append(data, data1...)

	service.conn.WriteToUDP(data, service.addr)

	fmt.Println("sent block ", id, hex.EncodeToString(r1))

}

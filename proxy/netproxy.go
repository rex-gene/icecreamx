package proxy

import (
	"errors"
	"github.com/RexGene/icecreamx/utils"
	"github.com/golang/protobuf/proto"
	"log"
	"net"
	"runtime/debug"
	"time"
)

type ICloseNotifyRecvicer interface {
	PushCloseNotify(interface{})
}

type IParser interface {
	ParseAndHandle(proxy *NetProxy, cmdId uint, data []byte) error
}

var (
	ErrCatchException = errors.New("<NetProxy> catch exception")
	ErrRequestTooFast = errors.New("<NetProxy> request too fast")
)

const (
	BUFFER_SIZE = 65536
)

type LastRecord struct {
	lastTime  time.Time
	lastCmdId uint
}

func (self *LastRecord) mark(now time.Time, cmdId uint) {
	self.lastTime = now
	self.lastCmdId = cmdId
}

func (self *LastRecord) isValid(now time.Time, cmdId uint) bool {
	if now.Sub(self.lastTime) < 100*time.Microsecond {
		return false
	}

	return true
}

type NetProxy struct {
	conn       net.Conn
	buffer     *DataBuffer
	record     LastRecord
	isRunning  bool
	parser     IParser
	customData interface{}
	recvicer   ICloseNotifyRecvicer
}

func (self *NetProxy) Start() {
	if !self.isRunning {
		self.isRunning = true
		go self.read_execute()
	}

	time.Now()
}

func (self *NetProxy) Send(cmdId uint, msg proto.Message) error {
	data, err := proto.Marshal(msg)
	if err != nil {
		return err
	}

	sendBuffer := make([]byte, len(data)+HEADER_SIZE)
	copy(sendBuffer[HEADER_SIZE:], data)

	FillHeader(cmdId, sendBuffer)

	offset := 0
	dataLen := len(sendBuffer)
	for offset < dataLen {
		wirteSize, err := self.conn.Write(sendBuffer[offset:])
		if err != nil {
			return err
		}

		offset += wirteSize
	}

	return nil
}

func (self *NetProxy) Stop() {
	self.isRunning = false
	self.conn.Close()

	recvicer := self.recvicer
	if recvicer != nil {
		recvicer.PushCloseNotify(self)
	}
}

func (self *NetProxy) SetCustomData(data interface{}) {
	self.customData = data
}

func (self *NetProxy) GetCustomData() interface{} {
	return self.customData
}

func (self *NetProxy) read_parseAndHandle(cmdId uint, data []byte) (err error) {
	defer func() {
		if ex := recover(); ex != nil {
			debug.PrintStack()
			err = ErrCatchException
		}
	}()

	now := time.Now()
	if self.record.isValid(now, cmdId) {
		log.Println("[!] drop cmd id:", cmdId)
		return ErrRequestTooFast
	}

	self.record.mark(now, cmdId)

	return self.parser.ParseAndHandle(self, cmdId, data)
}

func (self *NetProxy) read_execute() {
	defer func() {
		utils.PrintRecover(recover())
		self.Stop()
	}()

	for self.isRunning {
		var buffer *DataBuffer
		if self.buffer != nil {
			buffer = self.buffer
			self.buffer = nil
		} else {
			buffer = NewDataBufferByData(make([]byte, BUFFER_SIZE))
		}

		size, err := self.conn.Read(buffer.GetDataTail())
		if err != nil {
			log.Println("[!]", err)
			self.Stop()
			continue
		}

		err = buffer.WriteSize(size)
		if err != nil {
			log.Println("[!]", err)
			continue
		}

	L:
		dataLen := buffer.GetDataLen()
		if dataLen < HEADER_SIZE {
			self.buffer = buffer
			continue
		}

		header := CheckSumAndGetHeader(buffer.GetDataHead())
		if header == nil {
			log.Println("[!] check sum error")
			buffer.Reset()
			continue
		}

		if dataLen < int(header.Len) {
			self.buffer = buffer
			continue
		}

		err = buffer.ReadSize(int(header.Len))
		if err != nil {
			log.Println("[!] header.Len invalid:", header.Len)
			buffer.Reset()
		}

		dataLen = buffer.GetDataLen()
		if dataLen == 0 {
			executeData := buffer.GetReadData()
			err = self.read_parseAndHandle(uint(header.CmdId), executeData[HEADER_SIZE:])
			if err != nil {
				log.Println("[!]", err, header.CmdId)
			}
		} else {
			executeData := buffer.GetReadData()
			SurplusDate := buffer.GetData()
			buffer = NewDataBufferAndCopyData(BUFFER_SIZE, SurplusDate)
			err = self.read_parseAndHandle(uint(header.CmdId), executeData[HEADER_SIZE:])
			if err != nil {
				log.Println("[!]", err, header.CmdId)
			}
			goto L
		}
	}
}

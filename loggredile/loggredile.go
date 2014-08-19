package loggredile

import (
	"fmt"
	"net/http"
	"time"

	"code.google.com/p/gogoprotobuf/proto"
	"github.com/cloudfoundry/loggregatorlib/logmessage"
	"github.com/gorilla/websocket"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
)

func StreamIntoGBuffer(addr string, path string, sourceName string, outBuf, errBuf *gbytes.Buffer) chan<- bool {
	outMessages := make(chan *logmessage.LogMessage)
	errMessages := make(chan *logmessage.LogMessage)

	stop := streamMessages(addr, path, outMessages, errMessages)

	go func() {
		outOpen := true
		errOpen := true

		for outOpen || errOpen {
			var message *logmessage.LogMessage

			select {
			case message, outOpen = <-outMessages:
				if !outOpen {
					outMessages = nil
					break
				}

				if message.GetSourceName() != sourceName {
					continue
				}

				outBuf.Write([]byte(string(message.GetMessage()) + "\n"))
			case message, errOpen = <-errMessages:
				if !errOpen {
					errMessages = nil
					break
				}

				if message.GetSourceName() != sourceName {
					continue
				}

				errBuf.Write([]byte(string(message.GetMessage()) + "\n"))
			}
		}
	}()

	return stop
}

func streamMessages(addr string, path string, outMessages, errMessages chan<- *logmessage.LogMessage) chan<- bool {
	stop := make(chan bool, 1)

	var ws *websocket.Conn
	i := 0
	for {
		var err error
		ws, _, err = websocket.DefaultDialer.Dial(
			fmt.Sprintf("ws://%s%s", addr, path),
			http.Header{},
		)
		if err != nil {
			i++
			if i > 10 {
				fmt.Printf("Unable to connect to Server in 100ms, giving up.\n")
				return nil
			}

			time.Sleep(10 * time.Millisecond)
			continue
		} else {
			break
		}
	}

	go func() {
		for {
			_, data, err := ws.ReadMessage()
			if err != nil {
				close(outMessages)
				close(errMessages)
				return
			}

			receivedMessage := &logmessage.LogMessage{}
			err = proto.Unmarshal(data, receivedMessage)
			Ω(err).ShouldNot(HaveOccurred())

			if receivedMessage.GetMessageType() == logmessage.LogMessage_OUT {
				outMessages <- receivedMessage
			} else {
				errMessages <- receivedMessage
			}
		}
	}()

	return stop
}

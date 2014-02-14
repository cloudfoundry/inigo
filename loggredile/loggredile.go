package loggredile

import (
	"fmt"
	"net/http"
	"time"

	"code.google.com/p/gogoprotobuf/proto"
	"github.com/cloudfoundry/loggregatorlib/logmessage"
	"github.com/gorilla/websocket"
	. "github.com/onsi/gomega"
)

func StreamMessages(port int, path string) (<-chan *logmessage.LogMessage, chan<- bool) {
	receivedMessages := make(chan *logmessage.LogMessage)
	stop := make(chan bool, 1)

	var ws *websocket.Conn
	i := 0
	for {
		var err error
		ws, _, err = websocket.DefaultDialer.Dial(
			fmt.Sprintf("ws://127.0.0.1:%d%s", port, path),
			http.Header{},
		)
		if err != nil {
			i++
			if i > 10 {
				fmt.Printf("Unable to connect to Server in 100ms, giving up.\n")
				return nil, nil
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
				close(receivedMessages)
				return
			}

			receivedMessage := &logmessage.LogMessage{}
			err = proto.Unmarshal(data, receivedMessage)
			Ω(err).ShouldNot(HaveOccurred())

			receivedMessages <- receivedMessage
		}
	}()

	go func() {
		for {
			err := ws.WriteMessage(websocket.BinaryMessage, []byte{42})
			if err != nil {
				break
			}

			select {
			case <-stop:
				ws.Close()
				return

			case <-time.After(1 * time.Second):
				// keep-alive
			}
		}
	}()

	return receivedMessages, stop
}

package main

import (
	"context"
	"github.com/icamys/recws"
	"log"
	"time"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	ws, err := recws.New(
		"wss://echo.websocket.org", nil,
		recws.WithKeepAliveTimeout(10*time.Second),
	)
	if err != nil {
		panic(err)
	}

	ws.Dial()

	go func() {
		time.Sleep(2 * time.Second)
		cancel()
	}()

	for {
		select {
		case <-ctx.Done():
			go ws.Close()
			log.Printf("Websocket closed %s", ws.GetURL())
			return
		default:
			if !ws.IsConnected() {
				log.Printf("Websocket disconnected %s", ws.GetURL())
				continue
			}

			if err := ws.WriteMessage(1, []byte("Incoming")); err != nil {
				log.Printf("Error: WriteMessage %s", ws.GetURL())
				return
			}

			_, message, err := ws.ReadMessage()
			if err != nil {
				log.Printf("Error: ReadMessage %s", ws.GetURL())
				return
			}

			log.Printf("Success: %s", message)
		}
	}
}

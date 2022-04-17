// Package recws provides websocket client based on gorilla/websocket
// that will automatically reconnect if the connection is dropped.
package recws

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jpillora/backoff"
)

const (
	closedState = iota
	connectingState
	connectedState
)

var (
	// ErrNotConnected is returned when the application read/writes
	// a message and the connection is closed
	ErrNotConnected = errors.New("websocket: not connected")
)

type RecConn interface {
	// Close closes the underlying network connection without
	// sending or waiting for a close frame.
	Close()

	// CloseAndReconnect closes the underlying connection and tries to reconnect.
	CloseAndReconnect()

	// Shutdown gracefully closes the connection by sending the websocket.CloseMessage.
	// The writeWait param defines the duration before the deadline of the write operation is hit.
	Shutdown(writeWait time.Duration)

	// ReadMessage is a helper method for getting a reader
	// using NextReader and reading from that reader to a buffer.
	//
	// If the connection is closed ErrNotConnected is returned.
	ReadMessage() (messageType int, message []byte, err error)

	// WriteMessage is a helper method for getting a writer using NextWriter,
	// writing the message and closing the writer.
	//
	// If the connection is closed ErrNotConnected is returned.
	WriteMessage(messageType int, data []byte) error

	// WriteJSON writes the JSON encoding of v to the connection.
	//
	// See the documentation for encoding/json Marshal for details about the
	// conversion of Go values to JSON.
	//
	// If the connection is closed ErrNotConnected is returned.
	WriteJSON(v interface{}) error

	// ReadJSON reads the next JSON-encoded message from the connection and stores
	// it in the value pointed to by v.
	//
	// See the documentation for the encoding/json Unmarshal function for details
	// about the conversion of JSON to a Go value.
	//
	// If the connection is closed ErrNotConnected is returned.
	ReadJSON(v interface{}) error

	// Dial creates a new client connection by calling DialContext with a background context.
	Dial()

	// DialContext creates a new client connection.
	DialContext(ctx context.Context)

	// GetURL returns connection url.
	GetURL() string

	// GetHTTPResponse returns the http response from the handshake.
	// Useful when WebSocket handshake fails,
	// so that callers can handle redirects, authentication, etc.
	GetHTTPResponse() *http.Response

	// GetDialError returns the last dialer error or nil on successful connection.
	GetDialError() error

	// IsConnected returns true if the websocket client is connected to the server.
	IsConnected() bool
}

// The recConn type represents a Reconnecting WebSocket connection.
type recConn struct {
	options   connOptions
	state     int
	mu        sync.RWMutex
	url       string
	reqHeader http.Header
	httpResp  *http.Response
	dialErr   error
	dialer    *websocket.Dialer
	ctx       context.Context

	*websocket.Conn
}

// New creates new Reconnecting Websocket connection
// The `url` parameter specifies the host and request URI. Use `requestHeader` to specify
// the origin (Origin), subprotocols (Sec-WebSocket-Protocol) and cookies (Cookie).
// Use GetHTTPResponse() method for the response.Header to get
// the selected subprotocol (Sec-WebSocket-Protocol) and cookies (Set-Cookie).
func New(url string, requestHeader http.Header, options ...Option) (RecConn, error) {
	if valid, err := isValidUrl(url); !valid {
		return nil, fmt.Errorf("invalid url: %w", err)
	}

	const (
		defaultReconnectIntervalMin    = 2 * time.Second
		defaultReconnectIntervalMax    = 30 * time.Second
		defaultReconnectIntervalFactor = 1.5
		defaultHandshakeTimeout        = 2 * time.Second
		defaultKeepAliveTimeout        = 0
	)

	var (
		defaultProxy                            = http.ProxyFromEnvironment
		defaultOnConnectionCallback             = func() {}
		defaultTLSClientConfig      *tls.Config = nil
		defaultLogFnDebug                       = func(s string) {}
		defaultLogFnError                       = func(err error, s string) {}
	)

	rc := recConn{
		options: connOptions{
			ReconnectIntervalMin:    defaultReconnectIntervalMin,
			ReconnectIntervalMax:    defaultReconnectIntervalMax,
			ReconnectIntervalFactor: defaultReconnectIntervalFactor,
			HandshakeTimeout:        defaultHandshakeTimeout,
			Proxy:                   defaultProxy,
			TLSClientConfig:         defaultTLSClientConfig,
			OnConnectCallback:       defaultOnConnectionCallback,
			KeepAliveTimeout:        defaultKeepAliveTimeout,
			LogFn: logFnOptions{
				Debug: defaultLogFnDebug,
				Error: defaultLogFnError,
			},
		},
		url:       url,
		reqHeader: requestHeader,
		state:     closedState,
	}

	for _, opt := range options {
		opt(&rc.options)
	}

	rc.dialer = &websocket.Dialer{
		HandshakeTimeout: rc.options.HandshakeTimeout,
		Proxy:            rc.options.Proxy,
		TLSClientConfig:  rc.options.TLSClientConfig,
	}

	return &rc, nil
}

func (rc *recConn) CloseAndReconnect() {
	rc.Close()
	go rc.connect(nil)
}

func (rc *recConn) Close() {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	if rc.state != connectedState {
		return
	}
	if err := rc.Conn.Close(); err != nil {
		rc.options.LogFn.Error(err, "websocket connection closing error")
	}
	rc.state = closedState
}

func (rc *recConn) Shutdown(writeWait time.Duration) {
	msg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
	err := rc.WriteControl(websocket.CloseMessage, msg, time.Now().Add(writeWait))
	if err != nil && err != websocket.ErrCloseSent {
		rc.options.LogFn.Error(err, "shutdown error")
		// If close message could not be sent, then close without the handshake.
		rc.Close()
	}
}

func (rc *recConn) ReadMessage() (messageType int, message []byte, err error) {
	err = ErrNotConnected
	if rc.IsConnected() {
		messageType, message, err = rc.Conn.ReadMessage()
		if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
			rc.Close()
			return messageType, message, nil
		}
		if err != nil {
			rc.CloseAndReconnect()
		}
	}

	return
}

func (rc *recConn) WriteMessage(messageType int, data []byte) error {
	err := ErrNotConnected
	if rc.IsConnected() {
		rc.mu.Lock()
		err = rc.Conn.WriteMessage(messageType, data)
		rc.mu.Unlock()
		if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
			rc.Close()
			return nil
		}
		if err != nil {
			rc.CloseAndReconnect()
		}
	}

	return err
}

func (rc *recConn) WriteJSON(v interface{}) error {
	err := ErrNotConnected
	if rc.IsConnected() {
		rc.mu.Lock()
		err = rc.Conn.WriteJSON(v)
		rc.mu.Unlock()
		if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
			rc.Close()
			return nil
		}
		if err != nil {
			rc.CloseAndReconnect()
		}
	}

	return err
}

func (rc *recConn) ReadJSON(v interface{}) error {
	err := ErrNotConnected
	if rc.IsConnected() {
		err = rc.Conn.ReadJSON(v)
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				rc.Close()
				return nil
			}
			rc.CloseAndReconnect()
		}
	}

	return err
}

func (rc *recConn) Dial() {
	rc.DialContext(context.Background())
}

func (rc *recConn) DialContext(ctx context.Context) {
	rc.ctx = ctx

	// Connect
	connectedCh := make(chan struct{})
	go rc.connect(connectedCh)
	defer close(connectedCh)

	// wait for first attempt, but only up to a point
	timer := time.NewTimer(rc.options.HandshakeTimeout)
	defer timer.Stop()

	// no default case means this select will block until one of these conditions is met.
	// this is still guaranteed to complete, since the fallback here is the timer
	select {
	// in this case, the dial error is deferred until rc.GetDialError()
	case <-timer.C:
		return
	case <-connectedCh:
		return
	}
}

func (rc *recConn) GetURL() string {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return rc.url
}

func (rc *recConn) makeBackoff() backoff.Backoff {
	return backoff.Backoff{
		Min:    rc.options.ReconnectIntervalMin,
		Max:    rc.options.ReconnectIntervalMax,
		Factor: rc.options.ReconnectIntervalFactor,
		Jitter: true,
	}
}

func (rc *recConn) writeControlPingMessage() error {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	return rc.Conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(10*time.Second))
}

func (rc *recConn) keepAlive() {
	var keepAliveResponse = new(keepAliveResponse)
	rc.mu.Lock()
	rc.Conn.SetPongHandler(func(msg string) error {
		keepAliveResponse.setLastResponse()
		return nil
	})
	rc.mu.Unlock()

	go func() {
		var ticker = time.NewTicker(rc.options.KeepAliveTimeout)
		defer ticker.Stop()
		for {
			if !rc.IsConnected() {
				continue
			}

			writeTime := time.Now()
			if err := rc.writeControlPingMessage(); err != nil {
				rc.options.LogFn.Error(err, "error in writing ping message")
			}
			tick := <-ticker.C
			pingPongDuration := keepAliveResponse.getLastResponse().Sub(writeTime)
			if pingPongDuration < 1*time.Millisecond {
				pingPongDuration = 1 * time.Millisecond
			}

			if tick.Sub(keepAliveResponse.getLastResponse().Add(pingPongDuration)) > rc.options.KeepAliveTimeout {
				rc.CloseAndReconnect()
				return
			}
		}
	}()
}

func (rc *recConn) setState(state int) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.state = state
}

func (rc *recConn) getState() int {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return rc.state
}

func (rc *recConn) connect(connCh chan<- struct{}) {
	defer func() {
		if connCh != nil {
			connCh <- struct{}{}
		}
	}()

	if rc.getState() == connectingState {
		return
	}
	rc.setState(connectingState)

	b := rc.makeBackoff()
	rand.Seed(time.Now().UTC().UnixNano())

	for {
		wsConn, httpResp, err := rc.dialer.DialContext(rc.ctx, rc.url, rc.reqHeader)

		rc.mu.Lock()
		rc.Conn = wsConn
		rc.dialErr = err
		if err == nil {
			rc.state = connectedState
		} else {
			rc.state = closedState
		}
		rc.httpResp = httpResp
		rc.mu.Unlock()

		if err == nil {
			rc.options.LogFn.Debug(fmt.Sprintf("dial: connection was successfully established with %s\n", rc.url))
			rc.options.OnConnectCallback()
			if rc.options.KeepAliveTimeout != 0 {
				rc.keepAlive()
			}
			return
		}

		if rc.ctx.Err() != nil {
			rc.options.LogFn.Error(rc.ctx.Err(), "dial error")
			return
		}

		waitDuration := b.Duration()
		rc.options.LogFn.Error(err, fmt.Sprintf("dial error, will try again in %d seconds", waitDuration))
		time.Sleep(waitDuration)
	}
}

func (rc *recConn) GetHTTPResponse() *http.Response {
	rc.mu.RLock()
	defer rc.mu.RUnlock()

	return rc.httpResp
}

func (rc *recConn) GetDialError() error {
	rc.mu.RLock()
	defer rc.mu.RUnlock()

	return rc.dialErr
}

func (rc *recConn) IsConnected() bool {
	rc.mu.RLock()
	defer rc.mu.RUnlock()

	return rc.state == connectedState
}

// isValidUrl checks whether url is valid
func isValidUrl(urlStr string) (bool, error) {
	if urlStr == "" {
		return false, fmt.Errorf("url cannot be empty")
	}

	u, err := url.Parse(urlStr)

	if err != nil {
		return false, err
	}

	if u.Scheme != "ws" && u.Scheme != "wss" {
		return false, fmt.Errorf("websocket uris must start with ws or wss scheme")
	}

	if u.User != nil {
		return false, fmt.Errorf("user name and password are not allowed in websocket URIs")
	}

	return true, nil
}

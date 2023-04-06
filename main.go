package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"log"
	"net"
	"runtime/debug"
	"strings"
	"time"

	"github.com/valyala/fasthttp"
)

type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

var dialer DialFunc
var localInterfaceAddr = flag.String("i", "", "Out going Local Network Interface Address or Interface Name")
var bindAddr = flag.String("l", "127.0.0.1:8081", "Bind address")

var uncatchRecover = func() {
	if r := recover(); r != nil {
		log.Println("Uncatched error:", r, string(debug.Stack()))
	}
}

const httpClientTimeout = time.Minute
const dialTimeout = 20 * time.Second

var httpClient = &fasthttp.Client{
	ReadTimeout:         dialTimeout,
	MaxConnsPerHost:     233,
	MaxIdleConnDuration: 15 * time.Minute,
	ReadBufferSize:      1024 * 8,
	Dial: func(addr string) (net.Conn, error) {
		// no suitable address found => ipv6 can not dial to ipv4,..
		hostname, port, err := net.SplitHostPort(addr)
		if err != nil {
			if err1, ok := err.(*net.AddrError); ok && strings.Contains(err1.Err, "missing port") {
				hostname, port, err = net.SplitHostPort(strings.TrimRight(addr, ":") + ":80")
			}
			if err != nil {
				return nil, err
			}
		}
		if port == "" || port == ":" {
			port = "80"
		}
		return dialer(context.Background(), "tcp", "["+hostname+"]:"+port)
	},
}

func httpsHandler(ctx *fasthttp.RequestCtx, addr string) error {
	if ctx.Hijacked() {
		return nil
	}

	conn, err := dialer(context.Background(), "tcp", addr)
	if err != nil {
		return err
	}

	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.Response.Header.Set("Connection", "keep-alive")
	ctx.Response.Header.Set("Keep-Alive", "timeout=120, max=5")
	ctx.Hijack(func(clientConn net.Conn) {
		go ioTransfer(clientConn, conn)
		ioTransfer(conn, clientConn)
	})
	return nil
}

func ioTransfer(destination net.Conn, source net.Conn) {
	defer func() {
		uncatchRecover()
		time.Sleep(time.Second)
		source.Close()
		destination.Close()
	}()
	buf := make([]byte, 8192)
	for {
		source.SetReadDeadline(time.Now().Add(dialTimeout))
		nr, err := source.Read(buf)
		if nr > 0 {
			buf = buf[:nr]
			destination.SetWriteDeadline(time.Now().Add(dialTimeout))
			nw, err := destination.Write(buf)
			if nr != nw {
				break
			}
			if err != nil {
				// log.Println("ioTransfer w", err)
				break
			}
		}
		if err != nil {
			if err != io.EOF {
				// log.Println("ioTransfer r", err)
			}
			break
		}
	}
}

func requestHandler(ctx *fasthttp.RequestCtx) {
	defer uncatchRecover()
	// Some library must set header: Connection: keep-alive
	// ctx.Response.Header.Del("Connection")
	// ctx.Response.ConnectionClose() // ==> false

	// https connecttion
	if bytes.Equal(ctx.Method(), []byte("CONNECT")) {
		host := string(ctx.RequestURI())
		hostname, port, err := net.SplitHostPort(host)
		if err != nil {
			if err1, ok := err.(*net.AddrError); ok && strings.Contains(err1.Err, "missing port") {
				hostname, port, err = net.SplitHostPort(host + ":443")
			}
			if err != nil {
				ctx.SetStatusCode(fasthttp.StatusBadRequest)
				log.Println("Reject: Invalid host", host, err)
				return
			}
		}

		err = httpsHandler(ctx, "["+hostname+"]:"+port)
		if err != nil {
			ctx.SetStatusCode(fasthttp.StatusInternalServerError)
			log.Println("httpsHandler:", host, err)
		}
		return
	}

	err := httpClient.DoTimeout(&ctx.Request, &ctx.Response, httpClientTimeout)
	if err != nil {
		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		log.Println("httpHandler:", string(ctx.Host()), err)
	}
}

func getDialFunc(localAddr string) (DialFunc, error) {
	netInterfaces, err := net.Interfaces()
	if err != nil {
		log.Panicln(err)
	}

	var ips string
	for _, netInterface := range netInterfaces {
		addrs, err := netInterface.Addrs()
		if err != nil {
			log.Println(netInterface.Name, err)
			continue
		}
		isIfn := netInterface.Name == *localInterfaceAddr
		for _, addr := range addrs {
			ipAddr := addr.String()
			idx := strings.IndexRune(ipAddr, '/')
			if idx != -1 {
				ipAddr = ipAddr[0:idx]
			}
			ips += ", " + ipAddr
			if isIfn || ipAddr == *localInterfaceAddr {
				dialer := &net.Dialer{
					LocalAddr: &net.TCPAddr{
						IP: addr.(*net.IPNet).IP,
					},
					Timeout: dialTimeout,
				}
				return dialer.DialContext, nil
			}
		}
	}
	return nil, errors.New(`interface for ` + *localInterfaceAddr + ` not found` + ips)
}

func main() {
	flag.Parse()

	if len(*localInterfaceAddr) != 0 {
		var err error
		dialer, err = getDialFunc(*localInterfaceAddr)
		if err != nil {
			log.Panicln(err)
		}
		log.Println("Network going out interface:", *localInterfaceAddr)
	}

	if dialer == nil {
		dialer = (&net.Dialer{
			Timeout: dialTimeout,
		}).DialContext
	}

	server := &fasthttp.Server{
		Handler:              requestHandler,
		ReadTimeout:          dialTimeout, // 120s
		WriteTimeout:         dialTimeout,
		MaxKeepaliveDuration: time.Minute,
		MaxRequestBodySize:   20 * 1024 * 1024, // 20MB
		DisableKeepalive:     false,
	}

	log.Println("HTTP Proxy server is running at:", *bindAddr)
	log.Println("Usage: http://" + *bindAddr)
	log.Println("Eg: curl -x http://" + *bindAddr + " https://1.1.1.1/cdn-cgi/trace")
	if err := server.ListenAndServe(*bindAddr); err != nil {
		log.Panicln(err)
	}
}

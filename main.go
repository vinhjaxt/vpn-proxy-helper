package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-gost/gosocks5"
	"github.com/go-gost/gosocks5/server"
)

type DialFunc func(network, addr string) (net.Conn, error)

var localInterfaceAddr = flag.String(`i`, ``, `Out going Local Network Interface Address or Interface Name`)
var listen = flag.String(`l`, `127.0.0.1:1080`, `Bind address. Eg: unix:/tmp/.unix-socket/socks5.sock`)

var dialFuncAtomic = &atomic.Value{}
var dialFunc6Atomic = &atomic.Value{}
var lastLocalAddr = &atomic.Value{}
var lastLocal6Addr = &atomic.Value{}
var handler = &serverHandler{
	selector: server.DefaultSelector,
}

func loadDialFunc() {
	var lastIpv4 net.IP
	var lastIpv6 net.IP

	var tempIpv4 net.IP
	var tempIpv6 net.IP

	if v := lastLocalAddr.Load(); v != nil {
		lastIpv4 = lastLocalAddr.Load().(net.IP)
	}
	if v := lastLocal6Addr.Load(); v != nil {
		lastIpv6 = lastLocal6Addr.Load().(net.IP)
	}

	tempIpv4 = lastIpv4
	tempIpv6 = lastIpv6

	tcpDialer := &net.Dialer{
		DualStack: false,
		Timeout:   7 * time.Second,
	}
	tcpDialerV6 := &net.Dialer{
		DualStack: false,
		Timeout:   7 * time.Second,
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		log.Println("get localAddresses:", err)
		return
	}

	for _, i := range ifaces {
		if tcpDialer.LocalAddr != nil {
			break
		}
		addrs, err := i.Addrs()
		if err != nil {
			continue
		}
		// log.Println(`Interface:`, i.Name)
		if i.Name == *localInterfaceAddr {
			for _, a := range addrs {
				// log.Println(`IP:`, a.String())
				// log.Println(`Choose interface:`, i.Name, `, IP:`, a.String())
				var ip net.IP
				switch v := a.(type) {
				case *net.IPAddr:
					{
						ip = v.IP
					}

				case *net.IPNet:
					{
						ip = v.IP
					}
				}
				if ip.IsLinkLocalUnicast() {
					continue
				}
				if ip.To4() != nil {
					if !tempIpv4.Equal(ip) {
						tempIpv4 = ip
						tcpDialer.LocalAddr = &net.TCPAddr{
							IP:   ip,
							Port: 0,
						}
					}
				} else {
					if !tempIpv6.Equal(ip) {
						tempIpv6 = ip
						tcpDialerV6.LocalAddr = &net.TCPAddr{
							IP:   ip,
							Port: 0,
						}
					}
				}
				if tcpDialer.LocalAddr != nil && tcpDialerV6.LocalAddr != nil {
					break
				}
			}
			continue
		}
		for _, a := range addrs {
			// log.Println(`IP:`, a.String())
			var ip net.IP
			switch v := a.(type) {
			case *net.IPAddr:
				{
					ip = v.IP
				}

			case *net.IPNet:
				{
					ip = v.IP
				}
			}

			if ip.Equal(net.ParseIP(*localInterfaceAddr)) {
				// log.Println(`Choose interface:`, i.Name, `, IP:`, a.String())
				if ip.To4() != nil {
					if !tempIpv4.Equal(ip) {
						tempIpv4 = ip
						tcpDialer.LocalAddr = &net.TCPAddr{
							IP:   ip,
							Port: 0,
						}
					}
				} else {
					if !tempIpv6.Equal(ip) {
						tempIpv6 = ip
						tcpDialerV6.LocalAddr = &net.TCPAddr{
							IP:   ip,
							Port: 0,
						}
					}
				}
				break
			}
		}
	}

	if !tempIpv4.Equal(lastIpv4) {
		lastLocalAddr.Store(tempIpv4)
		dialFuncAtomic.Store(DialFunc(tcpDialer.Dial))
		log.Println(`change local ipv4`, tempIpv4)
	}

	if !tempIpv6.Equal(lastIpv6) {
		lastLocal6Addr.Store(tempIpv6)
		dialFunc6Atomic.Store(DialFunc(tcpDialerV6.Dial))
		log.Println(`change local ipv6`, tempIpv6)
	}

}

func serve(l net.Conn) {
	err := handler.Handle(l)
	if err != nil {
		log.Println(err)
	}
}

func main() {
	flag.Parse()

	var err error
	var ln net.Listener
	if strings.HasPrefix(*listen, `unix:`) {
		unixFile := (*listen)[5:]
		os.Remove(unixFile)
		ln, err = net.Listen(`unix`, unixFile)
		os.Chmod(unixFile, os.ModePerm)
		log.Println(`Listening:`, unixFile)
	} else {
		ln, err = net.Listen(`tcp`, *listen)
		if ln != nil {
			log.Println(`Listening:`, ln.Addr().String())
		}
	}
	if err != nil {
		log.Panicln(err)
	}
	if ln == nil {
		log.Panicln(`Error listening:`, *listen)
	}

	log.Println("Socks5 server is running at:", *listen)
	log.Println("Usage: socks5://" + *listen)
	log.Println("Eg: curl -x socks5://" + *listen + " https://1.1.1.1/cdn-cgi/trace")

	go func() {
		for {
			loadDialFunc()
			time.Sleep(2 * time.Second)
		}
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Println(err)
			continue
		}
		go serve(conn)
	}

}

type serverHandler struct {
	selector gosocks5.Selector
}

func (h *serverHandler) Handle(conn net.Conn) error {
	conn = gosocks5.ServerConn(conn, h.selector)
	req, err := gosocks5.ReadRequest(conn)
	if err != nil {
		return err
	}

	switch req.Cmd {
	case gosocks5.CmdConnect:
		return h.handleConnect(conn, req)

	default:
		return fmt.Errorf("%d: unsupported command", gosocks5.CmdUnsupported)
	}
}

func (h *serverHandler) handleConnect(conn net.Conn, req *gosocks5.Request) error {

	log.Println(`=> `, req.Addr.String())

	var dialFunc DialFunc

	if req.Addr.Type == gosocks5.AddrIPv6 {
		if v := dialFunc6Atomic.Load(); v == nil {
			return errors.New(`no dial6 func`)
		} else {
			dialFunc = v.(DialFunc)
		}
	} else {
		if v := dialFuncAtomic.Load(); v == nil {
			return errors.New(`no dial func`)
		} else {
			dialFunc = v.(DialFunc)
		}
	}

	cc, err := dialFunc("tcp", req.Addr.String())
	if err != nil {
		rep := gosocks5.NewReply(gosocks5.HostUnreachable, nil)
		rep.Write(conn)
		return err
	}
	defer cc.Close()

	rep := gosocks5.NewReply(gosocks5.Succeeded, nil)
	if err := rep.Write(conn); err != nil {
		return err
	}

	return transport(conn, cc)
}

var (
	trPool = sync.Pool{
		New: func() interface{} {
			return make([]byte, 1500)
		},
	}
)

func transport(rw1, rw2 io.ReadWriter) error {
	errc := make(chan error, 1)
	go func() {
		buf := trPool.Get().([]byte)
		defer trPool.Put(buf)

		_, err := io.CopyBuffer(rw1, rw2, buf)
		errc <- err
	}()

	go func() {
		buf := trPool.Get().([]byte)
		defer trPool.Put(buf)

		_, err := io.CopyBuffer(rw2, rw1, buf)
		errc <- err
	}()

	err := <-errc
	if err != nil && err == io.EOF {
		err = nil
	}
	return err
}

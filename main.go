package main

import (
	"encoding/binary"
	"errors"
	"flag"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type DialFunc func(network, addr string) (net.Conn, error)

var localInterfaceAddr = flag.String(`i`, ``, `Out going Local Network Interface Address or Interface Name`)
var listen = flag.String(`l`, `127.0.0.1:1080`, `Bind address. Eg: unix:/tmp/.unix-socket/socks5.sock`)

var dialFuncAtomic = &atomic.Value{}
var dialFunc6Atomic = &atomic.Value{}
var lastLocalAddr = &atomic.Value{}
var lastLocal6Addr = &atomic.Value{}

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
	if tcpDialer.LocalAddr == nil && tcpDialerV6.LocalAddr == nil {
		log.Println(`local interface name or ip not found`)
		return
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

var zeroTime time.Time

func handleConn(l net.Conn) error {
	defer l.Close()
	l.SetReadDeadline(time.Now().Add(3 * time.Second))

	{
		sockVer := make([]byte, 255)
		l.Read(sockVer)
		if !(sockVer[0] == 0x05) {
			return errors.New(`not socks5 protocol`)
		}
	}

	/*
		{
			sockVer := make([]byte, 2)
			l.Read(sockVer)
			if !(sockVer[0] == 0x05) {
				return errors.New(`not socks5 protocol`)
			}
			log.Println(sockVer)

			if sockVer[1] != 0 {
				l.Read(make([]byte, sockVer[1]))
			}
		}
		// */

	l.Write([]byte{0x05 /* ver */, 0x00 /* no-auth*/})

	connectCmd := make([]byte, 5)
	l.Read(connectCmd)

	// log.Println(`socks5 connect cmd:`, connectCmd)
	if !(connectCmd[0] == 0x05 && connectCmd[1] == 0x01 && connectCmd[2] == 0x00) {
		return errors.New(`not connect cmd`)
	}

	var dstAddrStr string
	dstIsV6 := false
	switch connectCmd[3] {
	case 0x01:
		{ // ipv4
			d := make([]byte, 4)
			d[0] = connectCmd[4]
			l.Read(d[1:])

			ip := net.IP(d)
			dstAddrStr = ip.String()
			dstIsV6 = ip.To4() == nil
		}
	case 0x03:
		{ //domain
			domainLen := int(connectCmd[4])
			domain := make([]byte, domainLen)
			l.Read(domain)

			dstAddrStr = string(domain)
		}
	case 0x04:
		{ //ipv6
			d := make([]byte, 16)
			d[0] = connectCmd[4]
			l.Read(d[1:])

			ip := net.IP(d)
			dstAddrStr = ip.String()
			dstIsV6 = ip.To4() == nil
		}
	default:
		{
			return errors.New(`not support address type`)
		}
	}

	dstPort := make([]byte, 2)
	l.Read(dstPort)

	dstPortStr := strconv.FormatUint(uint64(binary.BigEndian.Uint16(dstPort)), 10)

	log.Println(`=> [` + dstAddrStr + `]:` + dstPortStr)

	var dialFunc DialFunc

	if dstIsV6 {
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

	r, err := dialFunc(`tcp`, `[`+dstAddrStr+`]:`+dstPortStr)
	if err != nil {
		l.Write([]byte{0x05, 0x04 /*status*/, 0x00 /*reversed*/, 0x01, 0x00, 0x00, 0x00, 0x00 /* bind address*/, 0x00, 0x00 /* port */})
		return err
	}
	defer r.Close()

	l.Write([]byte{0x05, 0x00 /*status*/, 0x00 /*reversed*/, 0x01, 0x00, 0x00, 0x00, 0x00 /* bind address*/, 0x00, 0x00 /* port */})

	l.SetReadDeadline(zeroTime)
	go io.CopyBuffer(l, r, make([]byte, 4096))
	io.CopyBuffer(r, l, make([]byte, 4096))
	time.Sleep(2 * time.Second)

	return nil
}

func serve(l net.Conn) {
	err := handleConn(l)
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

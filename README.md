# VPN Proxy Helper
* Sometimes, VPN clients do not change the routing table of the computer but it still exists the VPN interface.
* Sometimes, you don't want to transfer all your network over a VPN Network.
It's time for time you might want a VPN Proxy Helper - a proxy server when out going traffic is over the local network interface (including VPN interface).

# vpn-proxy-helper --help
```
Usage of vpn-proxy-helper:
  -i string
        Out going Local Network Interface Address
  -l string
        Bind address (default "127.0.0.1:1080")
```

# Usage: socks5://127.0.0.1:1080
Eg:
```
curl -x socks5://127.0.0.1:1080 https://1.1.1.1/cdn-cgi/trace
```

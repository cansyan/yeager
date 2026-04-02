# Yeager

A proxy tool that helps speed up your internet connection in certain situations.
The tool provides HTTP and SOCKS5 proxy as entry points and supports gRPC/HTTP2/Shadowsocks transport.

How it works:

```
browser request -> [HTTP proxy -> yeager client] -> firewall -> [yeager server] -> endpoints
```

## As remote server
Firstly update firewall and allows TCP port 57175.

You may want to enable BBR (a TCP congestion control alogorithm) to improve unstable network:
```sh
$ sudo sysctl -w net.core.default_qdisc=fq
$ sudo sysctl -w net.ipv4.tcp_congestion_control=bbr
```

### Running with command line
```sh
$ wget https://github.com/cansyan/yeager/releases/latest/download/yeager-linux-amd64.tar.gz
$ tar -xzvf yeager-linux-amd64.tar.gz 
$ ./yeager -genconf
generated server.json
generated client.json
$ ./yeager -config server.json
```

### Running with systemd
```sh
$ wget https://github.com/cansyan/yeager/releases/latest/download/yeager-linux-amd64.tar.gz
$ tar -xzvf yeager-linux-amd64.tar.gz 
$ sudo cp yeager /usr/local/bin/yeager
$ sudo mkdir -p /usr/local/etc/yeager
$ cd /usr/local/etc/yeager
$ sudo /usr/local/bin/yeager -genconf
generated server.json
generated client.json
# the client.json will be used later
```

create file `/etc/systemd/system/yeager.service` with the following content:
```
[Unit]
Description=yeager
Documentation=https://github.com/cansyan/yeager
After=network.target

[Service]
ExecStart=/usr/local/bin/yeager -config /usr/local/etc/yeager/server.json
TimeoutStopSec=5s
LimitNOFILE=1048576
# can bind privileged ports, e.g. 443
# AmbientCapabilities=CAP_NET_BIND_SERVICE

[Install]
WantedBy=multi-user.target
```

start service:
```sh
$ sudo systemctl daemon-reload
$ sudo systemctl enable yeager
$ sudo systemctl start yeager
```

## As local client

### Running with command line

Copy `client.json` to local device.

Download [binary](https://github.com/cansyan/yeager/releases/latest), extract and run:
```sh
$ ./yeager -config client.json
```

SOCKS server address is 127.0.0.1:1080, HTTP proxy server address is 127.0.0.1:8080

### Running with launchd on macOS
> Let yeager run in the background when the operating system starts

Copy `client.json` to `/usr/local/etc/yeager.json`

Install:
```sh
$ curl -LO https://github.com/cansyan/yeager/releases/latest/download/yeager-macos-amd64.tar.gz
$ tar -xzvf yeager-macos-amd64.tar.gz
x yeager
x README.md
x LICENSE
$ cp yeager /usr/local/bin/yeager
``` 

Create file `~/Library/LaunchAgents/yeager.plist` with the following content:
```
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
  <dict>
    <key>KeepAlive</key>
    <dict>
        <key>SuccessfulExit</key>
        <false/>
    </dict>
    <key>RunAtLoad</key>
    <true/>
    <key>Label</key>
    <string>yeager</string>
    <key>ProgramArguments</key>
    <array>
      <string>/usr/local/bin/yeager</string>
      <string>-config</string>
      <string>/usr/local/etc/yeager.json</string>
    </array>
    <key>StandardErrorPath</key>
    <string>/usr/local/var/log/yeager.log</string>
  </dict>
</plist>
```

Run:
```sh
$ launchctl load ~/Library/LaunchAgents/yeager.plist
```

## Credit

- [grpc/grpc-go](https://github.com/grpc/grpc-go)
- [Jigsaw-Code/outline-sdk](https://github.com/Jigsaw-Code/outline-sdk)

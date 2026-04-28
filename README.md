# Yeager

Features:
- multiple transports, failover automatically
- bypass or block hosts

Usage:
```sh
go build .
./yeager -listen=socks://127.0.0.1:1080 -proxy=ss://method:password@host:port
```

For advanced configuration, see `config.go`. Create a `config.json`, then run:
```sh
./yeager -c config.json
```

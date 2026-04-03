# Yeager

Features:
- local HTTP, SOCKS5 proxy server
- multiple transports
- automatically selects the best server by URL test
- bypass or block hosts

## Usage

create `config.json`, for example 
```json
{
    "listen": [
        "socks5://127.0.0.1:1080",
        "http://127.0.0.1:8080"
    ],
    "proxy": [
        {
            "protocol": "ss",
            "address": "host:port",
            "secret": "password",
            "cipher": "aes-256-gcm"
        },
        {
            "protocol": "vmess",
            "address": "host:port",
            "secret": "uuid",
            "cipher": "auto"
        }
    ]
}
```

Build and run:
```sh
go build .
./yeager -c config.json
``` 
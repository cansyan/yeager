import os

def install():
    os.system("wget https://github.com/cansyan/yeager/releases/latest/download/yeager-linux-amd64.tar.gz")
    os.system("tar -xvf yeager-linux-amd64.tar.gz")
    os.system("cp yeager /usr/local/bin/yeager")
    cfgdir = "/usr/local/etc/yeager"
    if not os.path.exists(cfgdir+"/"+"server.json"):
        os.makedirs(cfgdir, exist_ok=True)
        os.chdir(cfgdir)
        os.system("/usr/local/bin/yeager -genconf")

serviceConfig = '''
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
'''

def run():
    with open("/etc/systemd/system/yeager.service", "w") as f:
        f.write(serviceConfig)
    os.system("systemctl daemon-reload")
    os.system("systemctl enable yeager")
    os.system("systemctl start yeager")
    
def bbr():
    os.system("sysctl -w net.core.default_qdisc=fq")
    os.system("sysctl -w net.ipv4.tcp_congestion_control=bbr")

if __name__ == "__main__":
    if os.geteuid() != 0:
        print("Please run as root")
        exit(1)
    install()
    run()
    bbr()
    print("Installation finished, please update firewall and allow TCP port 57175")

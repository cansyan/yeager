package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/chenen3/yeager/config"
	"github.com/chenen3/yeager/logger"
)

var version string // set by build -ldflags

func main() {
	var flags struct {
		configFile string
		version    bool
		genConfig  bool
		ip         string
		verbose    bool
		pprofHTTP  string
	}
	flag.StringVar(&flags.configFile, "config", "", "path to configuration file")
	flag.BoolVar(&flags.verbose, "v", false, "verbose logging")
	flag.BoolVar(&flags.version, "version", false, "print version")
	flag.BoolVar(&flags.genConfig, "genconf", false, "generate config")
	flag.StringVar(&flags.ip, "ip", "", "IP for the certificate, using with option -genconf")
	flag.Parse()

	if flags.verbose {
		logger.Debug.SetOutput(os.Stderr)
	}
	if flags.version {
		fmt.Printf("yeager version %s\n", version)
		return
	}

	if flags.genConfig {
		ip := flags.ip
		if ip == "" {
			i, err := checkIP()
			if err != nil {
				fmt.Printf("get public IP: %s\n", err)
				return
			}
			ip = i
		}
		if err := genConfig(ip, "client.json", "server.json"); err != nil {
			fmt.Println(err)
			return
		}
		return
	}

	if flags.configFile == "" {
		flag.Usage()
		return
	}
	bs, err := os.ReadFile(flags.configFile)
	if err != nil {
		logger.Error.Printf("read config: %s", err)
		return
	}
	var conf config.Config
	if err = json.Unmarshal(bs, &conf); err != nil {
		logger.Error.Printf("load config: %s", err)
		return
	}

	logger.Info.Printf("starting yeager %s", version)
	stop, err := start(conf)
	if err != nil {
		logger.Error.Printf("start service: %s", err)
		return
	}
	defer stop()

	if flags.pprofHTTP != "" {
		go func() {
			logger.Info.Println(http.ListenAndServe(flags.pprofHTTP, nil))
		}()
	}

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
	sig := <-ch
	logger.Info.Printf("received %s", sig)
}

func checkIP() (string, error) {
	resp, err := http.Get("https://checkip.amazonaws.com")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	ip, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(ip)), nil
}

func genConfig(host, cliConfOutput, srvConfOutput string) error {
	if _, err := os.Stat(srvConfOutput); err == nil {
		return fmt.Errorf("file %s already exists, operation aborted", srvConfOutput)
	}
	if _, err := os.Stat(cliConfOutput); err == nil {
		return fmt.Errorf("file %s already exists, operation aborted", cliConfOutput)
	}

	cliConf, srvConf, err := config.Generate(host)
	if err != nil {
		return fmt.Errorf("failed to generate config: %s", err)
	}
	bs, err := json.MarshalIndent(srvConf, "", "\t")
	if err != nil {
		return fmt.Errorf("failed to marshal server config: %s", err)
	}
	if err = os.WriteFile(srvConfOutput, bs, 0644); err != nil {
		return fmt.Errorf("failed to write server config: %s", err)
	}
	fmt.Println("generated", srvConfOutput)

	bs, err = json.MarshalIndent(cliConf, "", "\t")
	if err != nil {
		return fmt.Errorf("failed to marshal client config: %s", err)
	}
	err = os.WriteFile(cliConfOutput, bs, 0644)
	if err != nil {
		return fmt.Errorf("failed to write client config: %s", err)
	}
	fmt.Println("generated", cliConfOutput)
	return nil
}

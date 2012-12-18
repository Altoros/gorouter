package router

import (
	"io/ioutil"
	"launchpad.net/goyaml"
	"net/url"
	vcap "router/common"
)

var config Config

type Config struct {
	Port              uint16
	SessionKey        string
	Index             uint
	Status            StatusConfig
	Nats              NatsConfig
	Log               LogConfig "logging"
	FlushAppsInterval int       "flush_apps_interval,omitempty"
	GoMaxProcs        int       "go_max_procs,omitempty"
	ProxyWarmupTime   int       "proxy_warmup_time,omitempty"
	WaitBeforeExiting int       "wait_before_exiting,omitempty"

	ip string
}

type StatusConfig struct {
	Port     uint16
	User     string
	Password string
}

type NatsConfig struct {
	URI  string
	Host string
	User string
	Pass string
}

type LogConfig struct {
	Level  string
	File   string
	Syslog string
}

func InitConfigFromFile(configFile string) {
	configBytes, err := ioutil.ReadFile(configFile)
	if err != nil {
		log.Fatal(err.Error())
	}

	err = goyaml.Unmarshal(configBytes, &config)
	if err != nil {
		log.Fatal(err.Error())
	}

	SanitizeConfig(&config)
}

func InitConfig(c *Config) {
	SanitizeConfig(c)

	config = *c
}

func SanitizeConfig(config *Config) *Config {
	if config.Nats.URI != "" {
		u, err := url.Parse(config.Nats.URI)
		if err != nil {
			log.Fatal(err.Error())
		}

		config.Nats.Host = u.Host
		if u.User != nil {
			config.Nats.User = u.User.Username()
			config.Nats.Pass, _ = u.User.Password()
		}
	}

	if config.Nats.Host == "" {
		log.Fatal("nats server not configured")
	}

	config.ip, _ = vcap.LocalIP()

	return config
}

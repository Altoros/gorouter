package router

import (
	"bytes"
	"compress/zlib"
	"encoding/json"
	"fmt"
	nats "github.com/cloudfoundry/gonats"
	"net"
	"os"
	"os/signal"
	vcap "router/common"
	"router/proxy"
	"runtime"
	"syscall"
	"time"
)

type Router struct {
	proxy      *Proxy
	natsClient *nats.Client
	varz       *Varz
	registry   *Registry
}

func NewRouter() *Router {
	router := new(Router)

	// setup number of procs
	if config.GoMaxProcs != 0 {
		runtime.GOMAXPROCS(config.GoMaxProcs)
	}

	// setup nats
	router.natsClient = startNATS(config.Nats.Host, config.Nats.User, config.Nats.Pass)

	// setup varz
	router.varz = NewVarz()

	router.registry = NewRegistry()
	router.proxy = NewProxy(router.varz, router.registry)

	router.varz.Registry = router.registry

	varz := &vcap.Varz{
		UniqueVarz: router.varz,
	}

	component := &vcap.VcapComponent{
		Type:        "Router",
		Index:       config.Index,
		Host:        host(),
		Credentials: []string{config.Status.User, config.Status.Password},
		Config:      config,
		Logger:      log,
		Varz:        varz,
		Healthz:     "ok",
	}

	vcap.Register(component, router.natsClient)

	return router
}

func (r *Router) SubscribeRegister() {
	s := r.natsClient.NewSubscription("router.register")
	s.Subscribe()

	go func() {
		for m := range s.Inbox {
			var rm registerMessage

			e := json.Unmarshal(m.Payload, &rm)
			if e != nil {
				log.Warnf("unable to unmarshal %s : %s", string(m.Payload), e)
				continue
			}

			log.Debugf("router.register: %#v", rm)
			r.registry.Register(&rm)
		}
	}()
}

func (r *Router) SubscribeUnregister() {
	s := r.natsClient.NewSubscription("router.unregister")
	s.Subscribe()

	go func() {
		for m := range s.Inbox {
			var rm registerMessage

			e := json.Unmarshal(m.Payload, &rm)
			if e != nil {
				log.Warnf("unable to unmarshal %s : %s", string(m.Payload), e)
				continue
			}

			log.Debugf("router.unregister: %#v", rm)
			r.registry.Unregister(&rm)
		}
	}()
}

func (r *Router) flushApps(t time.Time) {
	x := r.registry.ActiveSince(t)

	y, err := json.Marshal(x)
	if err != nil {
		log.Warnf("json.Marshal: %s", err)
		return
	}

	b := bytes.Buffer{}
	w := zlib.NewWriter(&b)
	w.Write(y)
	w.Close()

	z := b.Bytes()

	log.Debugf("Active apps: %d, message size: %d", len(x), len(z))

	r.natsClient.Publish("router.active_apps", z)
}

func (r *Router) ScheduleFlushApps() {
	if config.FlushAppsInterval == 0 {
		return
	}

	go func() {
		t := time.NewTicker(time.Duration(config.FlushAppsInterval) * time.Second)
		n := time.Now()

		for {
			select {
			case <-t.C:
				n_ := time.Now()
				r.flushApps(n)
				n = n_
			}
		}
	}()
}

func (r *Router) Run() {
	var err error

	// Subscribe register/unregister router
	r.SubscribeRegister()
	r.SubscribeUnregister()

	// Start message
	r.natsClient.Publish("router.start", []byte(""))

	// Schedule flushing active app's app_id
	r.ScheduleFlushApps()

	l, err := net.Listen("tcp", fmt.Sprintf(":%d", config.Port))
	if err != nil {
		log.Fatalf("net.Listen: %s", err)
	}
	tl := proxy.NewTrackingListener(l)

	finished := make(chan bool)
	go func() {
		c := make(chan os.Signal)
		signal.Notify(c, syscall.SIGUSR1)
		<-c

		log.Info("Closing listener...")
		tl.Close()
		tl.WaitForConnectionsClosed()
		finished <- true
	}()

	s := proxy.Server{Handler: r.proxy}
	if config.ProxyWarmupTime != 0 {
		log.Info("Warming up proxy server ...")
		time.Sleep(time.Duration(config.ProxyWarmupTime) * time.Second)
	}
	err = s.Serve(tl)

	var waitAtMost = 120 * time.Second // wait for at most 120 seconds before exiting
	if config.WaitBeforeExiting != 0 {
		waitAtMost = time.Duration(config.WaitBeforeExiting) * time.Second
	}
	select {
	case <-time.After(waitAtMost):
		log.Error(err.Error())
	case <-finished:
		log.Info("Shutdown gracefully")
	}
}

func startNATS(host, user, pass string) *nats.Client {
	c := nats.NewClient()

	go func() {
		e := c.RunWithDefaults(host, user, pass)
		log.Fatalf("Failed to connect to nats server: %s", e.Error())
	}()

	return c
}

func host() string {
	if config.Status.Port == 0 {
		return ""
	}

	ip, err := vcap.LocalIP()
	if err != nil {
		log.Fatal(err.Error())
	}

	return fmt.Sprintf("%s:%d", ip, config.Status.Port)
}

package bootstrap

import (
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"gorm.io/gorm"
)

func TestManagerGraph(t *testing.T) {
	if err := fx.ValidateApp(Module, fx.NopLogger); err != nil {
		t.Fatal(err)
	}
}

func TestManagerHTTPStartsAndDrains(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()

	upstream, err := url.Parse("http://127.0.0.1:49151")
	if err != nil {
		t.Fatal(err)
	}
	app := fxtest.New(t, Module, fx.Replace(Config{Address: address, LogLevel: "info", GatewayToken: "test"}),
		fx.Replace(&gorm.DB{}), fx.Replace(upstream), fx.NopLogger)
	app.RequireStart()
	client := &http.Client{Timeout: time.Second}
	res, err := client.Get("http://" + address + "/health")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", res.StatusCode)
	}
	app.RequireStop()
	if res, err := client.Get("http://" + address + "/health"); err == nil {
		res.Body.Close()
		t.Fatal("HTTP listener accepted a request after Fx shutdown")
	}
}

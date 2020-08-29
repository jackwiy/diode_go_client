// Diode Network Client
// Copyright 2019 IoT Blockchain Technology Corporation LLC (IBTC)
// Licensed under the Diode License, Version 1.0
package rpc

import (
	"bytes"
	"fmt"
	"math/rand"
	"net"
	"runtime"
	"testing"
	"time"

	"github.com/diodechain/diode_go_client/config"
	"github.com/diodechain/diode_go_client/db"
	"github.com/diodechain/diode_go_client/util"
)

var (
	tunnelSize = 256
	// letter      = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	letterBytes = []byte("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
)

func newTestE2EServer(remoteConn net.Conn, peer Address, idleTimeout time.Duration) (e2eServer E2EServer) {
	e2eServer.remoteConn = remoteConn
	e2eServer.peer = peer
	e2eServer.idleTimeout = idleTimeout
	e2eServer.client = &RPCClient{
		logger: config.AppConfig.Logger,
	}
	e2eServer.closeCh = make(chan struct{})
	return
}

func testConfig() (cfg *config.Config) {
	cfg = &config.Config{
		DBPath:          util.DefaultDBPath(),
		RetryTimes:      3,
		EnableEdgeE2E:   false,
		EnableUpdate:    true,
		EnableMetrics:   false,
		Debug:           false,
		EnableAPIServer: false,
		APIServerAddr:   "localhost:1081",
		LogFilePath:     "",
		LogDateTime:     false,
		LogMode:         config.LogToFile,
		EnableKeepAlive: runtime.GOOS != "windows",
		KeepAliveCount:  4,
	}
	keepaliveIdleTime, _ := time.ParseDuration("30s")
	cfg.KeepAliveIdle = keepaliveIdleTime
	keepaliveIntervalTime, _ := time.ParseDuration("5s")
	cfg.KeepAliveInterval = keepaliveIntervalTime
	remoteRPCTimeoutTime, _ := time.ParseDuration("5s")
	cfg.RemoteRPCTimeout = remoteRPCTimeoutTime
	retryWaitTime, _ := time.ParseDuration("1s")
	cfg.RetryWait = retryWaitTime
	l, _ := config.NewLogger(cfg)
	cfg.Logger = &l
	return
}

func randomData(total, count int) (transportData [][]byte) {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	ll := len(letterBytes)
	for i := 0; i < total; i++ {
		data := make([]byte, count)
		for j := 0; j < count; j++ {
			ind := r.Int() % ll
			data[j] = letterBytes[ind]
		}
		transportData = append(transportData, data)
	}
	return
}

func TestE2ETunnels(t *testing.T) {
	cfg := testConfig()
	config.AppConfig = cfg
	clidb, err := db.OpenFile(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	db.DB = clidb
	ca, cb := net.Pipe()
	defer ca.Close()
	defer cb.Close()

	pubKey := LoadClientPubKey()
	ID := util.PubkeyToAddress(pubKey)

	// random test data
	transportData := randomData(10, tunnelSize)
	errCh := make(chan error)
	// client
	go func() {
		// fack proxy client and server
		fc, fs := net.Pipe()
		defer fc.Close()
		defer fs.Close()
		// e2e server for fc and fs
		e2eServer := newTestE2EServer(fs, ID, 2*time.Second)
		defer e2eServer.Close()
		err := e2eServer.InternalClientConnect()
		if err != nil {
			errCh <- err
			return
		}

		// copy local tunnel
		tunnel := NewTunnel(e2eServer.localConn, 1*time.Second, ca, 1*time.Second, tunnelSize)
		go tunnel.Copy()
		var n int
		for i := 0; i < 10; i += 2 {
			n, err = fc.Write(transportData[i])
			if err != nil {
				errCh <- err
				return
			}
			if n != len(transportData[i]) {
				errCh <- fmt.Errorf("Data was truncated when write to e2e in client")
				return
			}
			buf := make([]byte, tunnelSize)
			_, err = fc.Read(buf)
			if err != nil {
				errCh <- err
				return
			}
			if !bytes.Equal(buf, transportData[i+1]) {
				errCh <- fmt.Errorf("Data was truncated when read from e2e in client")
				return
			}
		}
		// the last receiver should close the error channel
		errCh <- nil
	}()
	// device
	go func() {
		// fack device client and server
		fc, fs := net.Pipe()
		defer fc.Close()
		defer fs.Close()
		// e2e client for fc and fs
		e2eServer := newTestE2EServer(fs, ID, 2*time.Second)
		defer e2eServer.Close()
		err := e2eServer.InternalServerConnect()
		if err != nil {
			errCh <- err
			return
		}
		// copy local tunnel to c
		tunnel := NewTunnel(e2eServer.localConn, 1*time.Second, cb, 1*time.Second, tunnelSize)
		go tunnel.Copy()
		if err != nil {
			errCh <- err
			return
		}
		var n int
		for i := 1; i < 10; i += 2 {
			buf := make([]byte, tunnelSize)
			_, err = fc.Read(buf)
			if err != nil {
				errCh <- err
				return
			}
			if !bytes.Equal(buf, transportData[i-1]) {
				errCh <- fmt.Errorf("Data was truncated when read from e2e in device")
				return
			}
			n, err = fc.Write(transportData[i])
			if err != nil {
				errCh <- err
				return
			}
			if n != len(transportData[i]) {
				errCh <- fmt.Errorf("Data was truncated when write to e2e in device")
				return
			}
		}
	}()
	err = <-errCh
	if err != nil {
		t.Fatal(err)
	}
}

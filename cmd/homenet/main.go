package main

import (
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/denisbrodbeck/machineid"

	"github.com/xaionaro-go/homenet-peer/config"
	"github.com/xaionaro-go/homenet-peer/connector"
	"github.com/xaionaro-go/homenet-peer/helpers"
	"github.com/xaionaro-go/homenet-peer/negotiator"
	"github.com/xaionaro-go/homenet-peer/network"
	"github.com/xaionaro-go/homenet-peer/vpn"
	"github.com/xaionaro-go/homenet-server/api"
)

const (
	MachineIDLength = 8
)

func fatalIf(err error) {
	if err != nil {
		logrus.Fatalf("%s", err.Error())
	}
}

type debugLogger struct{}

func (l *debugLogger) Printf(fmt string, args ...interface{}) {
	logrus.Debugf(fmt, args...)
}

func (l *debugLogger) Print(args ...interface{}) {
	logrus.Debug(args...)
}

func main() {
	logrus.SetLevel(logrus.DebugLevel)

	if config.Get().DumpConfiguration {
		logrus.Debugf("Configuration == %v", config.Get())
	}

	var apiOptions api.Options
	if config.Get().DumpAPICommunications {
		apiOptions = append(apiOptions, api.OptSetLoggerDebug(&debugLogger{}))
	}

	passwordFile := config.Get().PasswordFile
	password, err := ioutil.ReadFile(passwordFile)
	if err != nil {
		panic(fmt.Errorf(`cannot read the password file "%v"`, passwordFile))
	}

	networkID := config.Get().NetworkID
	passwordHashHash := string(helpers.Hash([]byte(strings.Trim(string(password), " \t\n\r"))))
	homenetServer := api.New(config.Get().ArbitrURL, passwordHashHash, apiOptions...)
	status, netInfo, err := homenetServer.GetNet(networkID)
	fatalIf(err)
	switch status {
	case http.StatusOK:
	case http.StatusNotFound:
		status, netInfo, err = homenetServer.RegisterNet(networkID)
	default:
		panic(fmt.Errorf("received an unexpected HTTP status code from the arbitr: %v", status))
	}

	var vpnOptions vpn.Options
	if config.Get().DumpVPNCommunications {
		vpnOptions = append(vpnOptions, vpn.OptSetLoggerDump(&debugLogger{}))
	}

	_, subnet, err := net.ParseCIDR(config.Get().NetworkSubnet)
	fatalIf(err)

	netLogger := &logger{config.Get().DumpNetworkCommunications}

	homenet, err := network.New(nil, netLogger)
	fatalIf(err)

	connectorInstance := connector.New(negotiator.New(config.Get().NetworkUpdateInterval, homenetServer, networkID, homenet, netLogger), netLogger)

	homenet.SetConnector(connectorInstance)

	_, err = vpn.New(*subnet, homenet, vpnOptions...)
	fatalIf(err)

	hostname, _ := os.Hostname()
	machineID, _ := machineid.ProtectedID("homenet-peer")
	if len(machineID) > MachineIDLength {
		machineID = machineID[:MachineIDLength]
	}
	peerName := hostname + "_" + machineID
	if peerName == "_" {
		peerName = ""
	}

	_, _, err = homenetServer.RegisterPeer(netInfo.GetID(), homenet.GetPeerID(), peerName, homenet.GetIdentity().Keys.Public)
	fatalIf(err)

	_, peers, err := homenetServer.GetPeers(netInfo.GetID())
	fatalIf(err)
	fatalIf(homenet.UpdatePeers(peers))

	ticker := time.NewTicker(config.Get().NetworkUpdateInterval)
	for {
		<-ticker.C
		_, _, err = homenetServer.RegisterPeer(netInfo.GetID(), homenet.GetPeerID(), peerName, homenet.GetIdentity().Keys.Public)
		if err != nil {
			logrus.Errorf("homenetServer.RegisterPeer(%s, %s): %s", netInfo.GetID(), homenet.GetPeerID(), err.Error())
		}
		_, peers, err := homenetServer.GetPeers(netInfo.GetID())
		if err != nil {
			logrus.Errorf("homenetServer.GetPeers(%s): %s", netInfo.GetID(), err.Error())
		}
		err = homenet.UpdatePeers(peers)
		if err != nil {
			logrus.Errorf("homenet.UpdatePeers(): %s", err.Error())
		}
	}
}

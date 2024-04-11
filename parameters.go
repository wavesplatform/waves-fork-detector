package main

import (
	"errors"
	"flag"
	"fmt"
	"math"
	rand2 "math/rand/v2"
	"net"
	"net/netip"
	"os"
	"path"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/wavesplatform/gowaves/pkg/proto"
	"github.com/wavesplatform/gowaves/pkg/settings"
)

type parameters struct {
	logLevel        zapcore.Level
	dbPath          string
	scheme          proto.Scheme
	genesis         proto.Block
	seedPeers       []*net.TCPAddr
	apiBind         string
	netBind         proto.TCPAddr
	declaredAddress proto.TCPAddr
	name            string
	nonce           uint32
	versions        []proto.Version
}

func newParameters() (*parameters, error) {
	ll := zap.LevelFlag("log-level", zapcore.InfoLevel,
		"Specify the logging level. Supported levels include: DEBUG, INFO, WARN, ERROR, and FATAL.")
	flagDB := flag.String("db", "",
		"Specify the path to the database folder. There is no default value. Please, provide one.")
	flagBlockchainType := flag.String("blockchain-type", "mainnet",
		"Specify the blockchain type: mainnet, testnet, or stagenet.")
	flagPeers := flag.String("peers", "",
		"This option forces the node to connect to the provided peers. "+
			"Specify peers in the format: \"ip:port,...,ip:port\".")
	flagAPI := flag.String("api", "127.0.0.1:8080",
		"The local network address to bind the HTTP API of the service. The default value is \"127.0.0.1:8080\".")
	flagNet := flag.String("net", "127.0.0.1:6868",
		"The local network address to bind the network server. The default value is \"127.0.0.1:6868\".")
	flagDeclaredAddress := flag.String("declared-address", "",
		"The network address presented to the network as a public address for connection.")
	flagName := flag.String("name", "forkdetector",
		"The name of the node visible to the network. Default value: \"forkdetector\".")
	flagVersions := flag.String("versions", "1.5,1.4",
		"Specify a comma-separated list of acceptable protocol versions. "+
			"By default, the two most recent versions are accepted.")
	flag.Parse()
	dbf, err := checkFolder(*flagDB)
	if err != nil {
		return nil, fmt.Errorf("invalid parameters: invalid database folder: %w", err)
	}
	bs, err := settings.BlockchainSettingsByTypeName(*flagBlockchainType)
	if err != nil {
		return nil, fmt.Errorf("invalid parameters: failed to get blockchain settings: %w", err)
	}
	sps, err := parseSeedPeers(*flagPeers, bs.Type)
	if err != nil {
		return nil, fmt.Errorf("invalid parameters: %w", err)
	}
	vss, err := parseVersions(*flagVersions)
	if err != nil {
		return nil, fmt.Errorf("invalid parameters: %w", err)
	}
	aa, err := checkBind(*flagAPI)
	if err != nil {
		return nil, fmt.Errorf("invalid parameters: %w", err)
	}
	na, err := checkBind(*flagNet)
	if err != nil {
		return nil, fmt.Errorf("invalid parameters: %w", err)
	}
	nm, err := checkName(*flagName)
	if err != nil {
		return nil, fmt.Errorf("invalid parameters: %w", err)
	}
	return &parameters{
		logLevel:        *ll,
		dbPath:          dbf,
		scheme:          bs.AddressSchemeCharacter,
		genesis:         bs.Genesis,
		seedPeers:       sps,
		apiBind:         aa,
		netBind:         parseAddress(na),
		declaredAddress: parseAddress(*flagDeclaredAddress),
		name:            nm,
		nonce:           rand2.Uint32(),
		versions:        vss,
	}, nil
}

func (p *parameters) log() {
	zap.S().Debugf("Configuration parameters:")
	zap.S().Debugf("\tlogLevel: %s", p.logLevel)
	zap.S().Debugf("\tdbPath: %s", p.dbPath)
	zap.S().Debugf("\tscheme: %c", p.scheme)
	zap.S().Debugf("\tgenesis: %s", p.genesis.BlockID().String())
	zap.S().Debugf("\tseedPeers: %v", p.seedPeers)
	zap.S().Debugf("\tapiBind: %s", p.apiBind)
	zap.S().Debugf("\tnetBind: %s", p.netBind)
	zap.S().Debugf("\tdeclaredAddress: %s", p.declaredAddress)
	zap.S().Debugf("\tname: %s", p.name)
	zap.S().Debugf("\tversions: %s", p.versions)
}

func parseSeedPeers(seedPeers string, blockchainType settings.BlockchainType) ([]*net.TCPAddr, error) {
	var r []*net.TCPAddr
	switch blockchainType {
	case settings.MainNet:
		r = []*net.TCPAddr{
			net.TCPAddrFromAddrPort(netip.MustParseAddrPort("34.253.153.4:6868")),
			net.TCPAddrFromAddrPort(netip.MustParseAddrPort("168.119.116.189:6868")),
			net.TCPAddrFromAddrPort(netip.MustParseAddrPort("135.181.87.72:6868")),
			net.TCPAddrFromAddrPort(netip.MustParseAddrPort("162.55.39.115:6868")),
			net.TCPAddrFromAddrPort(netip.MustParseAddrPort("168.119.155.201:6868")),
		}
	case settings.TestNet:
		r = []*net.TCPAddr{
			net.TCPAddrFromAddrPort(netip.MustParseAddrPort("159.69.126.149:6868")),
			net.TCPAddrFromAddrPort(netip.MustParseAddrPort("94.130.105.239:6868")),
			net.TCPAddrFromAddrPort(netip.MustParseAddrPort("159.69.126.153:6868")),
			net.TCPAddrFromAddrPort(netip.MustParseAddrPort("94.130.172.201:6868")),
			net.TCPAddrFromAddrPort(netip.MustParseAddrPort("35.157.247.122:6868")),
		}
	case settings.StageNet:
		r = []*net.TCPAddr{
			net.TCPAddrFromAddrPort(netip.MustParseAddrPort("88.99.185.128:6868")),
			net.TCPAddrFromAddrPort(netip.MustParseAddrPort("49.12.15.166:6868")),
			net.TCPAddrFromAddrPort(netip.MustParseAddrPort("95.216.205.3:6868")),
			net.TCPAddrFromAddrPort(netip.MustParseAddrPort("88.198.179.16:6868")),
			net.TCPAddrFromAddrPort(netip.MustParseAddrPort("52.58.254.101:6868")),
		}
	case settings.Custom:
		return nil, errors.New("failed to parse peers: Custom blockchain type is unsupported")
	default:
		return nil, fmt.Errorf("failed to parse peers: unsupported blockchain type: %v", blockchainType)
	}
	sp := strings.Split(seedPeers, ",")
	for _, a := range sp {
		a = strings.TrimSpace(a)
		if len(a) == 0 {
			continue
		}
		ap, err := netip.ParseAddrPort(strings.TrimSpace(a))
		if err != nil {
			return nil, fmt.Errorf("failed to parse peers: %w", err)
		}
		r = append(r, net.TCPAddrFromAddrPort(ap))
	}
	return r, nil
}

func parseVersions(str string) ([]proto.Version, error) {
	parts := strings.Split(str, ",")
	r := make([]proto.Version, 0, len(parts))
	for _, p := range parts {
		v, err := proto.NewVersionFromString(p)
		if err != nil {
			return nil, fmt.Errorf("invalid versions: %w", err)
		}
		r = append(r, v)
	}
	return r, nil
}

func checkFolder(str string) (string, error) {
	if str == "" {
		return "", errors.New("empty folder")
	}
	fi, err := os.Stat(str)
	if os.IsNotExist(err) {
		return "", fmt.Errorf("folder does not exist: %s", str)
	}
	if err != nil {
		return "", fmt.Errorf("invalid folder: %w", err)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("not a folder: %s", str)
	}
	return path.Clean(str), nil
}

func checkBind(str string) (string, error) {
	if str == "" {
		return "", errors.New("empty address")
	}
	ap, err := netip.ParseAddrPort(str)
	if err != nil {
		return "", fmt.Errorf("invalid address: %w", err)
	}
	return ap.String(), nil
}

func checkName(str string) (string, error) {
	if str == "" {
		return "", errors.New("empty name")
	}
	if len(str) > math.MaxUint8 {
		return "", errors.New("name is too long")
	}
	return str, nil
}

func parseAddress(addr string) proto.TCPAddr {
	ap, err := netip.ParseAddrPort(addr)
	if err != nil {
		return proto.TCPAddr{}
	}
	ip4 := ap.Addr().As4()
	ip := net.IPv4(ip4[0], ip4[1], ip4[2], ip4[3])
	return proto.NewTCPAddr(ip, int(ap.Port()))
}

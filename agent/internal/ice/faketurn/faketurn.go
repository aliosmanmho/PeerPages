// Package faketurn testler icin surec ici TURN sunucusu saglar.
//
// Neden gerekli: Faz 5'in kabul kriteri "simetrik NAT'ta TURN ile
// baglaniyor". Simetrik NAT'i tek makinede taklit edemiyoruz, ama AYNI
// SONUCU ureten bir durum kurabiliyoruz: ajani yalnizca relay adayi
// toplamaya zorlayip (ice.Config.RelayOnly) buraya baglamak. O zaman
// dogrudan yol IMKANSIZ olur, yani tunelin acilmasi trafigin TURN'den
// gectiginin kanitidir — mobil hotspot denemesinden guclu, cunku orada
// dogrudan yol da calismis olabilir.
//
// internal/nostr/fakerelay ile ayni desen: gercek internete bagimli bir
// test kirilgan olur.
package faketurn

import (
	"errors"
	"fmt"
	"io"
	"net"
	"sync/atomic"

	"github.com/pion/logging"
	"github.com/pion/turn/v5"
)

// Realm TURN long-term credential realm'i.
const Realm = "peerpages.test"

// Options sahte sunucu ayarlari.
type Options struct {
	// User, Pass kabul edilen tek kimlik bilgisi.
	User, Pass string
}

// Server surec ici TURN sunucusu.
type Server struct {
	srv  *turn.Server
	conn net.PacketConn
	addr *net.UDPAddr

	allocations atomic.Int64
}

// Start bos bir portta baslatir.
func Start(opts Options) (*Server, error) { return StartOn(0, opts) }

// StartOn verilen portta baslatir (0 = bos port sec).
//
// Yalnizca 127.0.0.1 dinlenir: bu bir TEST sunucusu, aga acilmamali.
func StartOn(port int, opts Options) (*Server, error) {
	if opts.User == "" || opts.Pass == "" {
		return nil, errors.New("faketurn: kullanici ve sifre gerekli")
	}

	conn, err := net.ListenPacket("udp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, fmt.Errorf("faketurn: dinlenemedi: %w", err)
	}
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		_ = conn.Close()
		return nil, errors.New("faketurn: dinlenen adres okunamadi")
	}

	s := &Server{conn: conn, addr: addr}
	relayIP := localIPv4()

	key := turn.GenerateAuthKey(opts.User, Realm, opts.Pass)
	srv, err := turn.NewServer(turn.ServerConfig{
		Realm: Realm,
		AuthHandler: func(ra *turn.RequestAttributes) (string, []byte, bool) {
			if ra == nil || ra.Username != opts.User || ra.Realm != Realm {
				return "", nil, false
			}
			return ra.Username, key, true
		},
		PacketConnConfigs: []turn.PacketConnConfig{{
			PacketConn: conn,
			RelayAddressGenerator: &turn.RelayAddressGeneratorStatic{
				// Duyurulan relay adresi makinenin LAN adresi, soket ise
				// tum arayuzlere bagli.
				//
				// Neden 127.0.0.1 DEGIL: karsi taraf (pion ya da tarayici)
				// genelde loopback adayi uretmez, LAN adresini kullanir ve
				// 127.0.0.1'e bagli bir soket oraya paket gonderemez.
				// Ayrica duyurulan adres, relay'in paket gonderirken
				// kullandigi kaynak adresle AYNI olmali: aksi halde karsi
				// taraf gelen kontrolu "peer-reflexive" sayar ve secilen
				// aday cifti relay GORUNMEZ (gercek bir TURN sunucusunda
				// bu tutarlilik zaten var).
				RelayAddress: relayIP,
				Address:      "0.0.0.0",
			},
		}},
		EventHandler: turn.EventHandler{
			OnAllocationCreated: func(
				_, _ net.Addr, _, _, _ string, _ net.Addr, _ int,
			) {
				s.allocations.Add(1)
			},
		},
		// Testler hizli kosar; sunucu loglari gurultu.
		LoggerFactory: &logging.DefaultLoggerFactory{Writer: io.Discard},
	})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("faketurn: sunucu kurulamadi: %w", err)
	}
	s.srv = srv
	return s, nil
}

// URL ajana verilecek TURN adresi.
func (s *Server) URL() string {
	return fmt.Sprintf("turn:127.0.0.1:%d", s.addr.Port)
}

// Port dinlenen port (ayni adreste yeniden baslatmak icin).
func (s *Server) Port() int { return s.addr.Port }

// Allocations kac allocation verildi.
//
// Testin asil iddiasi bu: tunel acildi VE bu sayac arttiysa trafik
// gercekten TURN'den gecti.
func (s *Server) Allocations() int64 { return s.allocations.Load() }

// Close sunucuyu kapatir.
func (s *Server) Close() error {
	if s.srv == nil {
		return s.conn.Close()
	}
	return s.srv.Close()
}

// localIPv4 makinenin varsayilan cikis arayuzundeki IPv4 adresini bulur.
//
// Hicbir paket gonderilmiyor: UDP "baglantisi" yalnizca isletim sisteminin
// hangi arayuzu sececegini sormak icin. Bulunamazsa loopback'e duser
// (yalnizca loopback adayi uretilen ortamlarda bu da yeterli).
func localIPv4() net.IP {
	c, err := net.Dial("udp4", "203.0.113.1:9")
	if err != nil {
		return net.IPv4(127, 0, 0, 1)
	}
	defer func() { _ = c.Close() }()

	addr, ok := c.LocalAddr().(*net.UDPAddr)
	if !ok || addr.IP == nil {
		return net.IPv4(127, 0, 0, 1)
	}
	return addr.IP
}

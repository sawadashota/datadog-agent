// +build windows

package ebpf

/*
#include "c/ddfilterapi.h"
*/
import "C"
import (
	"expvar"
	"fmt"
	"golang.org/x/net/ipv4"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/DataDog/datadog-agent/pkg/process/util"
	"github.com/DataDog/datadog-agent/pkg/util/log"

	"golang.org/x/sys/windows"
)

const (
	// Number of buffers to use with the IOCompletion port to communicate with the driver
	totalReadBuffers = 32
)

var (
	expvarEndpoints map[string]*expvar.Map
	expvarTypes     = []string{"driver_total_stats", "driver_handle_stats", "packet_count"}
)

func init() {
	expvarEndpoints = make(map[string]*expvar.Map, len(expvarTypes))
	for _, name := range expvarTypes {
		expvarEndpoints[name] = expvar.NewMap(name)
	}
}

// readBuffer is the buffer to pass into ReadFile system call to pull out packets
type readBuffer struct {
	ol   windows.Overlapped
	data [128]byte
}

// Tracer struct for tracking network state and connections
type Tracer struct {
	config           *Config
	driverController *DriverInterface
	bufs             []readBuffer
	packetCount      int64
}

// NewTracer returns an initialized tracer struct
func NewTracer(config *Config) (*Tracer, error) {
	dc, err := NewDriverInterface()
	if err != nil {
		return nil, fmt.Errorf("could not create windows driver controller", err)
	}

	tr := &Tracer{
		driverController: dc,
		bufs:             make([]readBuffer, totalReadBuffers),
	}

	// We want tracer to own the buffers, but the DriverInterface to make the calls to set them up
	tr.bufs, err = dc.prepareReadBuffers(tr.bufs)
	if err != nil {
		return nil, fmt.Errorf("error preparing driver's read buffers")
	}

	err = tr.initPacketPolling()
	if err != nil {
		log.Warnf("issue polling packets from driver")
	}
	go tr.expvarStats()
	return tr, nil
}

// Stop function stops running tracer
func (t *Tracer) Stop() {}

func (t *Tracer) expvarStats() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	// starts running the body immediately instead waiting for the first tick
	for range ticker.C {
		stats, err := t.GetStats()
		if err != nil {
			continue
		}

		for name, stat := range stats {
			for metric, val := range stat.(map[string]int64) {
				currVal := &expvar.Int{}
				currVal.Set(val)
				expvarEndpoints[name].Set(snakeToCapInitialCamel(metric), currVal)
			}
		}
	}
}

func (t *Tracer) initPacketPolling() (err error) {
	log.Debugf("Started packet polling")
	go func() {
		var (
			bytes uint32
			key   uint32
			ol    *windows.Overlapped
		)

		for {
			err := windows.GetQueuedCompletionStatus(t.driverController.iocp, &bytes, &key, &ol, windows.INFINITE)
			if err == nil {
				var buf *readBuffer
				buf = (*readBuffer)(unsafe.Pointer(ol))
				numPackets := printPacket(*buf, bytes)
				atomic.AddInt64(&t.packetCount, numPackets)
				windows.ReadFile(t.driverController.driverHandle, buf.data[:], nil, &(buf.ol))
			}
		}
	}()
	return
}

// GetActiveConnections returns all active connections
func (t *Tracer) GetActiveConnections(_ string) (*Connections, error) {
	return &Connections{
		DNS: map[util.Address][]string{
			util.AddressFromString("127.0.0.1"): {"localhost"},
		},
		Conns: []ConnectionStats{
			{
				Source: util.AddressFromString("127.0.0.1"),
				Dest:   util.AddressFromString("127.0.0.1"),
				SPort:  35673,
				DPort:  8000,
				Type:   TCP,
			},
		},
	}, nil
}

// getConnections returns all of the active connections in the ebpf maps along with the latest timestamp.  It takes
// a reusable buffer for appending the active connections so that this doesn't continuously allocate
func (t *Tracer) getConnections(active []ConnectionStats) ([]ConnectionStats, uint64, error) {
	return nil, 0, ErrNotImplemented
}

// GetStats returns a map of statistics about the current tracer's internal state
func (t *Tracer) GetStats() (map[string]interface{}, error) {
	packetCount := atomic.LoadInt64(&t.packetCount)
	driverStats, err := t.driverController.getStats()
	if err != nil {
		log.Errorf("not printing driver stats: %v", err)
	}

	return map[string]interface{}{
		"packet_count": map[string]int64{
			"count": packetCount,
		},
		"driver_total_stats":  driverStats["driver_total_stats"],
		"driver_handle_stats": driverStats["driver_handle_stats"],
	}, nil
}

// DebugNetworkState returns a map with the current tracer's internal state, for debugging
func (t *Tracer) DebugNetworkState(clientID string) (map[string]interface{}, error) {
	return nil, ErrNotImplemented
}

// DebugNetworkMaps returns all connections stored in the maps without modifications from network state
func (t *Tracer) DebugNetworkMaps() (*Connections, error) {
	return nil, ErrNotImplemented
}

// CurrentKernelVersion is not implemented on this OS for Tracer
func CurrentKernelVersion() (uint32, error) {
	return 0, ErrNotImplemented
}

func printPacket(buf readBuffer, bytes uint32) int64 {
	var header ipv4.Header
	var packetHeader C.struct_filterPacketHeader

	dataStart := uint32(unsafe.Sizeof(packetHeader))
	pheader := *(*C.struct_filterPacketHeader)(unsafe.Pointer(&(buf.data[0])))

	log.Debugf("Contains %v packets \n", pheader.numPackets)

	for i := uint64(0); i < uint64(pheader.numPackets) && dataStart < bytes; i++ {
		header.Parse(buf.data[dataStart:])
		dataStart += 128
	}
	return int64(pheader.numPackets)

}
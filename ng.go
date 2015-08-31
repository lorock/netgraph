package main

import (
    "flag"
    "fmt"
    "github.com/ga0/netgraph/ngnet"
    "github.com/ga0/netgraph/ngserver"
    "github.com/google/gopacket"
    "github.com/google/gopacket/layers"
    "github.com/google/gopacket/pcap"
    "github.com/google/gopacket/pcapgo"
    "github.com/google/gopacket/tcpassembly"
    "os"
)

var eventChan chan interface{}
var inputPcap = flag.String("f", "", "Open pcap file")
var device = flag.String("i", "", "Device to capture, auto select one if no device provided")
var bindingPort = flag.Int("p", 9000, "Web server port")
var bpf = flag.String("bpf", "tcp port 80", "Berkeley Packet Filter")
var outputPcap = flag.String("o", "", "Output captured packet to pcap file")

func init() {
    flag.Parse()
    if *inputPcap != "" && *outputPcap != "" {
        fmt.Println("set -f and -o at the same time")
        os.Exit(-1)
    }
    if *inputPcap != "" && *device != "" {
        fmt.Println("set -f and -i at the same time")
        os.Exit(-1)
    }
    eventChan = make(chan interface{}, 1024)
}

func autoSelectDev() string {
    ifs, err := pcap.FindAllDevs()
    if err != nil {
        fmt.Println(err)
        os.Exit(-1)
    }
    /*fmt.Println("Auto select interface...")
      fmt.Println("[All interfaces]")
      for _, i := range ifs {
          fmt.Println(i.Name)
          for _, addr := range i.Addresses {
              fmt.Printf("    %s\n", addr.IP.String())
          }
      }*/
    var available []string
    //fmt.Println("[Available]")
    for _, i := range ifs {
        addrFound := false
        var addrs []string
        for _, addr := range i.Addresses {
            if addr.IP.IsLoopback() ||
                addr.IP.IsMulticast() ||
                addr.IP.IsUnspecified() ||
                addr.IP.IsLinkLocalUnicast() {
                continue
            }
            addrFound = true
            addrs = append(addrs, addr.IP.String())
        }
        if addrFound {
            available = append(available, i.Name)
            /*fmt.Println(i.Name)
              for _, addr := range addrs {
                  fmt.Printf("    %s\n", addr)
              }*/
        }
    }
    if len(available) > 0 {
        return available[0]
    }
    return ""
}

func packetSource() *gopacket.PacketSource {
    if *inputPcap != "" {
        handle, err := pcap.OpenOffline(*inputPcap)
        if err != nil {
            fmt.Println(err)
            os.Exit(-1)
        }
        fmt.Printf("open pcap file \"%s\"\n", *inputPcap)
        return gopacket.NewPacketSource(handle, handle.LinkType())
    }

    if *device == "" {
        *device = autoSelectDev()
        if *device == "" {
            fmt.Println("no packet to capture")
            os.Exit(-1)
        }
    }

    handle, err := pcap.OpenLive(*device, 1024*1024, true, pcap.BlockForever)
    if err != nil {
        fmt.Println(err)
        os.Exit(-1)
    }
    if *bpf != "" {
        if err = handle.SetBPFFilter(*bpf); err != nil {
            fmt.Println("Failed to set BPF filter:", err)
            os.Exit(-1)
        }
    }
    fmt.Printf("open live on device \"%s\", bpf \"%s\"\n", *device, *bpf)
    return gopacket.NewPacketSource(handle, handle.LinkType())
}

func runNGNet(packetSource *gopacket.PacketSource) {
    streamFactory := ngnet.NewHttpStreamFactory(eventChan)
    pool := tcpassembly.NewStreamPool(streamFactory)
    assembler := tcpassembly.NewAssembler(pool)

    var pcapWriter *pcapgo.Writer
    if *outputPcap != "" {
        outPcapFile, err := os.Create(*outputPcap)
        if err != nil {
            fmt.Println(err)
            os.Exit(-1)
        }
        defer outPcapFile.Close()
        pcapWriter = pcapgo.NewWriter(outPcapFile)
        pcapWriter.WriteFileHeader(65536, layers.LinkTypeEthernet)
    }

    var count int = 0
    for packet := range packetSource.Packets() {
        count++
        net_layer := packet.NetworkLayer()
        if net_layer == nil {
            continue
        }
        trans_layer := packet.TransportLayer()
        if trans_layer == nil {
            continue
        }
        tcp, _ := trans_layer.(*layers.TCP)
        if tcp == nil {
            continue
        }

        if pcapWriter != nil {
            pcapWriter.WritePacket(packet.Metadata().CaptureInfo, packet.Data())
        }

        assembler.AssembleWithTimestamp(
            net_layer.NetworkFlow(),
            tcp,
            packet.Metadata().CaptureInfo.Timestamp)
    }

    assembler.FlushAll()
    streamFactory.Wait()
    fmt.Println("Packet count: ", count)
}

func main() {
    go runNGNet(packetSource())
    addr := fmt.Sprintf(":%d", *bindingPort)
    s := ngserver.NewNGServer(addr, "client", eventChan)
    s.Serve()
}

package main

import (
	_ "embed"
	"log"
	"net"
	"net/http"
	"time"
	"flag"
	"fmt"
	"strings"
	// "sync"

	"github.com/bluenviron/mediacommon/pkg/formats/mpegts"

	"github.com/bluenviron/gohlslib"
	"github.com/bluenviron/gohlslib/pkg/codecs"
)

// This example shows how to:
// 1. generate a MPEG-TS/H264 stream with GStreamer
// 2. re-encode the stream into HLS and serve it with an HTTP server

//go:embed index.html
var index []byte

type UDPAddressInfo struct {
	Address string
	Name    string
	Resolution string
	Bandwidth string
}

// handleIndex wraps an HTTP handler and serves the home page
func handleIndex(wrapped http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(index))
			return
		}

		wrapped(w, r)
	}
}

func main() {
	// var wg sync.WaitGroup

	directory := flag.String("dir", "", "Directory for HLS files")
	// udpAddresses := flag.String("udps", "", "List of UDP addresses and names, formatted as 'address|name|resolution|bandwidth,address|name|resolution|bandwidth,...'")
	udpAddress := flag.String("udp", "localhost:9000", "List of UDP addresses and names, formatted as 'address|name|resolution|bandwidth,address|name|resolution|bandwidth,...'")


	flag.Parse()

	// udpInfo := parseUDPAddresses(*udpAddresses)

	header := "#EXTM3U\n#EXT-X-VERSION:9\n#EXT-X-INDEPENDENT-SEGMENTS\n\n"
	m3u8String := ""
	// create the HLS muxer
	mux := &gohlslib.Muxer{
		VideoTrack: &gohlslib.Track{
			Codec: &codecs.H264{},
		},
		Directory: *directory,
		SegmentCount: 999999999999,
	}
	err := mux.Start()
	if err != nil {
		panic(err)
	}


	// for _, info := range udpInfo {
	// 	fmt.Printf("Address: %s, Name: %s, Resolution: %s, Bandwidth: %s\n", info.Address, info.Name, info.Resolution, info.Bandwidth)
	// 	m3u8String += GenerateM3U8String(info.Bandwidth, info.Resolution)

	// 	pc, err := net.ListenPacket("udp", info.Address)
	// 	if err != nil {
	// 		panic(err)
	// 	}
	// 	defer pc.Close()
	
	// 	log.Println("Starting for ", info.Name, info.Address)
	// 	wg.Add(1)

	// 	go func(pc net.PacketConn, resolution string) {
	// 		defer wg.Done()
	// 		setupMPEGTSReader(pc, resolution)
	// 	}(pc, info.Name)
	// }


	m3u8String += GenerateM3U8String("1000000", "1280x720")
	mux.GenerateMainManifest(header + m3u8String)

	pc, err := net.ListenPacket("udp", *udpAddress)
	if err != nil {
		panic(err)
	}
	defer pc.Close()

	log.Println("Starting for ", "1280x720", udpAddress)
	setupMPEGTSReader(pc, "1280x720")
	// wg.Wait()
}


func setupMPEGTSReader(pc net.PacketConn, resolution string) {
	// create the HLS muxer
	mux := &gohlslib.Muxer{
		VideoTrack: &gohlslib.Track{
			Codec: &codecs.H264{},
		},
		Directory: "/Users/karthik/Downloads/hls/",
		SegmentCount: 999999,
		Prefix: resolution,
	}
	err := mux.Start()
	if err != nil {
		panic(err)
	}
	r, err := mpegts.NewReader(mpegts.NewBufferedReader(newPacketConnReader(pc)))
	if err != nil {
		panic(err)
	}

	var timeDec *mpegts.TimeDecoder

	// Find the H264 track and set up a callback
	found := false
	for _, track := range r.Tracks() {
		if _, ok := track.Codec.(*mpegts.CodecH264); ok {
			r.OnDataH26x(track, func(rawPTS int64, _ int64, au [][]byte) error {
				if timeDec == nil {
					timeDec = mpegts.NewTimeDecoder(rawPTS)
				}
				pts := timeDec.Decode(rawPTS)
				log.Printf("Encoding access unit for resolution = %v with PTS = %v", resolution, pts)
				mux.WriteH26x(time.Now(), pts, au)
				return nil
			})

			found = true
			break
		}
	}



	for {
		err := r.Read()
		if err != nil {
			panic(err)
		}
	}

	if !found {
		panic("H264 track not found")
	}

}

func parseUDPAddresses(input string) []UDPAddressInfo {
	var result []UDPAddressInfo
	if input == "" {
		return result
	}

	// Splitting the input string by comma to get each address|name pair
	pairs := strings.Split(input, ",")
	for _, pair := range pairs {
		// Splitting each pair by pipe
		parts := strings.Split(pair, "|")

		if len(parts) == 4 {
			result = append(result, UDPAddressInfo{Address: parts[0], Name: parts[1], Resolution: parts[2], Bandwidth: parts[3]})
		} else {
			fmt.Printf("Invalid UDP address format: '%s'\n", pair)
		}
	}
	return result
}


func GenerateM3U8String(bandwidth string, resolution string) string {
    var sb strings.Builder

    codecs := "avc1.42c01f"
    frameRate := "24.000"


    resSplit := strings.Split(resolution, "x")
    if len(resSplit) == 2 {
        resTag := resSplit[1]
        sb.WriteString(fmt.Sprintf("#EXT-X-STREAM-INF:BANDWIDTH=%s,AVERAGE-BANDWIDTH=%s,CODECS=\"%s\",RESOLUTION=%s,FRAME-RATE=%s\n",
            bandwidth, bandwidth, codecs, resolution, frameRate))
        sb.WriteString(fmt.Sprintf("video_%sp.m3u8\n\n", resTag))
    }

    return sb.String()
}
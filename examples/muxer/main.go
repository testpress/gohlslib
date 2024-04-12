package main

import (
	_ "embed"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bluenviron/mediacommon/pkg/codecs/mpeg4audio"
	"github.com/bluenviron/mediacommon/pkg/formats/mpegts"

	"github.com/bluenviron/gohlslib"
	"github.com/bluenviron/gohlslib/pkg/codecs"
)

// This example shows how to:
// 1. generate a MPEG-TS/H264 stream with GStreamer
// 2. re-encode the stream into HLS and serve it with an HTTP server

//go:embed index.html
var index []byte
var m3u8String string

type UDPAddressInfo struct {
	Address    string
	Name       string
	Resolution string
	Bandwidth  string
}

func handleM3u8Urls(muxMap map[string]*gohlslib.Muxer) http.HandlerFunc {
	// muxMap will have muxer for each resolution.
	// When url contains a resolution name then we will use that specific muxer from muxMap to handle that url.

	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/video.m3u8" {
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(m3u8String))
			return
		}

		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(index))
			return
		}

		for resolution, mux := range muxMap {
			if strings.Contains(r.URL.Path, resolution) {
				mux.Handle(w, r)
				return
			}
		}

		http.NotFound(w, r)
	}
}

func main() {
	var wg sync.WaitGroup
	directory := flag.String("dir", "", "Directory for HLS files")
	udpAddresses := flag.String("udps", "", "List of UDP addresses and names, formatted as 'address|name|resolution|bandwidth,address|name|resolution|bandwidth,...'")

	flag.Parse()
	header := "#EXTM3U\n#EXT-X-VERSION:9\n#EXT-X-INDEPENDENT-SEGMENTS\n\n"
	m3u8String = ""

	// create the HLS muxer
	mux := &gohlslib.Muxer{
		VideoTrack: &gohlslib.Track{
			Codec: &codecs.H264{},
		},
		AudioTrack: &gohlslib.Track{
			Codec: &codecs.MPEG4Audio{
				Config: mpeg4audio.Config{
					Type:         2,
					SampleRate:   44100,
					ChannelCount: 2,
				},
			},
		},
		Directory:    *directory,
		SegmentCount: 999999,
		Prefix:       "720p",
	}
	err := mux.Start()
	if err != nil {
		panic(err)
	}

	udpInfo := parseUDPAddresses(*udpAddresses)

	muxMap := make(map[string]*gohlslib.Muxer)

	for index, info := range udpInfo {
		fmt.Printf("Address: %s, Name: %s, Resolution: %s, Bandwidth: %s\n", info.Address, info.Name, info.Resolution, info.Bandwidth)
		m3u8String += GenerateM3U8String(info.Bandwidth, info.Resolution)

		pc, err := net.ListenPacket("udp", info.Address)
		if err != nil {
			panic(err)
		}
		defer pc.Close()

		log.Println("Starting for ", info.Name, info.Address)
		wg.Add(1)
		mux := &gohlslib.Muxer{
			VideoTrack: &gohlslib.Track{
				Codec: &codecs.H264{},
			},
			AudioTrack: &gohlslib.Track{
				Codec: &codecs.MPEG4Audio{
					Config: mpeg4audio.Config{
						Type:         2,
						SampleRate:   44100,
						ChannelCount: 2,
					},
				},
			},
			Directory:    *directory,
			SegmentCount: 999999,
			Prefix:       info.Name,
		}
		muxMap[info.Name] = mux
		go func(pc net.PacketConn, resolution string, mux *gohlslib.Muxer, index int) {
			defer wg.Done()
			setupMPEGTSReader(pc, resolution, *directory, mux, index)
		}(pc, info.Name, mux, index)
	}

	s := &http.Server{
		Addr:    ":8081",
		Handler: handleM3u8Urls(muxMap),
	}
	log.Println("HTTP server created on :8080")
	go s.ListenAndServe()

	m3u8String = header + m3u8String
	mux.GenerateMainManifest(m3u8String)
	wg.Wait()
}

func setupMPEGTSReader(pc net.PacketConn, resolution string, directory string, mux *gohlslib.Muxer, index int) {
	err := mux.Start()

	fmt.Println("Mux map adding for ", resolution)
	if err != nil {
		panic(err)
	}

	// create a MPEG-TS reader
	r, err := mpegts.NewReader(mpegts.NewBufferedReader(newPacketConnReader(pc)))
	if err != nil {
		panic(err)
	}

	var timeDec *mpegts.TimeDecoder

	// find the H264 track
	found := false
	for _, track := range r.Tracks() {

		if _, ok := track.Codec.(*mpegts.CodecH264); ok {
			// setup a callback that is called once a H264 access unit is received
			r.OnDataH26x(track, func(rawPTS int64, _ int64, au [][]byte) error {
				// decode the time
				if timeDec == nil {
					timeDec = mpegts.NewTimeDecoder(rawPTS)
				}
				pts := timeDec.Decode(rawPTS)

				// pass the access unit to the HLS muxer
				// log.Printf("visit http://localhost:8080 - encoding access unit with PTS = %v", pts)
				mux.WriteH26x(time.Now(), pts, au)

				return nil
			})
			found = true
		}

		if _, ok := track.Codec.(*mpegts.CodecMPEG4Audio); ok {
			r.OnDataMPEG4Audio(track, func(rawPTS int64, aus [][]byte) error {
				if timeDec == nil {
					timeDec = mpegts.NewTimeDecoder(rawPTS)
				}
				pts := timeDec.Decode(rawPTS)
				mux.WriteMPEG4Audio(time.Now(), pts, aus)
				return nil
			})
		}
	}

	if !found {
		panic("H264 track not found")
	}

	// read from the MPEG-TS stream
	for {
		err := r.Read()
		if err != nil {
			panic(err)
		}
	}
}

func printMuxMap(muxMap map[string]*gohlslib.Muxer) {
	fmt.Println("Contents of muxMap:")
	for resolution, mux := range muxMap {
		fmt.Printf("Resolution: %s, Mux: %v\n", resolution, mux)
	}
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
		sb.WriteString(fmt.Sprintf("stream_%sp.m3u8\n\n", resTag))
	}

	return sb.String()
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

package main

import (
	"fmt"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3/pkg/media"
	"github.com/pion/webrtc/v3/pkg/media/oggwriter"
)

// TODO: regen this and put in env before deploy!
var botToken = "ODI5NTk4MDcxMjAxMjAyMjI3.YG6daQ.bpvwnty2nkoWCZVCD1mctWZ3Gyc"
var guildId = "829599334127501312"
var channelId = "829599334127501316"


func createPionRTPPacket(p *discordgo.Packet) *rtp.Packet {
	return &rtp.Packet{
		Header: rtp.Header{
			Version: 2,
			// Taken from Discord voice docs
			PayloadType:    0x78,
			SequenceNumber: p.Sequence,
			Timestamp:      p.Timestamp,
			SSRC:           p.SSRC,
		},
		Payload: p.Opus,
	}
}


func handleVoice(v *discordgo.VoiceConnection, wg *sync.WaitGroup) {
	c := v.OpusRecv
	files := make(map[uint32]media.Writer)
	for p := range c {
		file, ok := files[p.SSRC]
		if !ok {
			var err error
			file, err = oggwriter.New(fmt.Sprintf("%d.ogg", p.SSRC), 48000, 2)
			if err != nil {
				fmt.Printf("failed to create file %d.ogg, giving up on recording: %v\n", p.SSRC, err)
				return
			}
			files[p.SSRC] = file
		}
		// Construct pion RTP packet from DiscordGo's type.
		rtp := createPionRTPPacket(p)
		err := file.WriteRTP(rtp)
		if err != nil {
			fmt.Printf("failed to write to file %d.ogg, giving up on recording: %v\n", p.SSRC, err)
		}

		v.OpusSend <- rtp.Payload
	}

	// Once we made it here, we're done listening for packets. Close all files
	for _, f := range files {
		f.Close()
	}	
	wg.Done()
}

func main() {
	s, err := discordgo.New("Bot "+ botToken);

	if err != nil {
		fmt.Println("can't init Discord sesh:", err)
		return
	}

	defer s.Close()

	// configure listener's tracked intents?
	s.Identify.Intents = discordgo.IntentsGuildVoiceStates

	err = s.Open()
	if err != nil {
		fmt.Println("can't open conn:", err)
		return
	}

	v, err := s.ChannelVoiceJoin(guildId, channelId, false, false);
	if err != nil {
		fmt.Println("can't join guild/channel:", err)
		return
	}

	wg := sync.WaitGroup{}

	wg.Add(1)
	handleVoice(v, &wg)
	go func() {
		time.Sleep(3 * time.Second)
		v.Close()
	}()
	wg.Wait()
}
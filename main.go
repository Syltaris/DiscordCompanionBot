package main

import (
	"fmt"
	"math/rand"
	"os"
	"sync"
	"time"

	"github.com/joho/godotenv"

	"github.com/bwmarrin/discordgo"
	"github.com/cdipaolo/sentiment"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3/pkg/media"
	"github.com/pion/webrtc/v3/pkg/media/oggwriter"
	witai "github.com/wit-ai/wit-go"
)

var witAiClient *witai.Client

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
			file, err = oggwriter.New(fmt.Sprintf("%d.ogg", p.SSRC), 48000, 1)
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

	}
	// Once we made it here, we're done listening for packets. Close all files
	for _, f := range files {
		f.Close()
	}	
	
	//
	file, err := os.Open("593398.ogg")
	if err != nil {
		fmt.Println("can't open file:" ,err)
	}
	// send to wit AI to parse
	speech := witai.Speech{File: file, ContentType: "audio/ogg"}
	msg, err := witAiClient.Speech(&witai.MessageRequest{Speech: &speech})
	if err != nil {
		fmt.Println("can't send to witAi:", err)
	}
	fmt.Println("output: ",msg.Text, msg)

	// temp debug code
	rand.Seed(time.Now().Unix())
	choices := []string{
		"get rekt noob",
		"wow yes i did it",
		"totally nice job",
		"wow you suck eggs",
		"eat my shorts",
	}
	outputText := choices[rand.Int() % len(choices)]

	// suppose we got the output here, feed it to sentiment engine and see result
	model, err := sentiment.Restore() 
		if err != nil {  
		panic(err) 
	} 
	var analysis *sentiment.Analysis
	analysis = model.SentimentAnalysis(outputText, sentiment.English)
	fmt.Println("score:", analysis.Score, outputText)
	if analysis.Score == 1{
		// play congrats sound
		
	} else {
		// play oh noes sound
	}

	wg.Done()
}

func main() {
	err := godotenv.Load(".env")
	botToken := os.Getenv("DISCORD_BOT_TOKEN")
	witAiToken := os.Getenv("WIT_AI_TOKEN")

	s, err := discordgo.New("Bot "+ botToken);
	if err != nil {
		fmt.Println("can't init Discord sesh:", err)
		return
	}
	defer s.Close()

	// init witAi client
	witAiClient = witai.NewClient(witAiToken)


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
	go func() {
		time.Sleep(3 * time.Second)
		close(v.OpusRecv)
		v.Close()
	}()
	handleVoice(v, &wg)
	wg.Wait()
}
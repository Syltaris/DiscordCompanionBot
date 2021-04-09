package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/Syltaris/DiscordCompanionBot/lib"
	"github.com/bwmarrin/discordgo"
	"github.com/cdipaolo/sentiment"
	"github.com/joho/godotenv"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3/pkg/media"
	"github.com/pion/webrtc/v3/pkg/media/oggwriter"
)


var guildId = "829599334127501312"
var channelId = "829599334127501316"

var sentimentModel sentiment.Models
var s *discordgo.Session


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

func HandleVoiceReceive(v *discordgo.VoiceConnection, messages chan uint32, wg *sync.WaitGroup) {
	defer wg.Done()

	c := v.OpusRecv
	files := make(map[uint32]media.Writer)
	stop := make(chan bool)

	closeFiles := false

	go func() {
		for {
			time.Sleep(5 * time.Second)
			stop <- true
		}
	}()

	for p := range c {
		select {
			case <- stop:
				fmt.Println("break")
				closeFiles = true
				break
			default:
				file, ok := files[p.SSRC]
				if !ok {
					var err error
					file, err = oggwriter.New(fmt.Sprintf("userAudio/%d.ogg", p.SSRC), 48000, 2)
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

		if closeFiles == true {
			// Once we made it here, we're done listening for packets. Close all files
			fmt.Println("closing files...")
			for k, f := range files {
				err := f.Close()
				if err != nil {
					fmt.Println("what's this", err)
				}
				messages <- k
			}	
			// re-init the files?
			files = make(map[uint32]media.Writer)
			closeFiles = false
		}
	}
}


func HandleBotReply(v *discordgo.VoiceConnection, messages chan uint32, wg *sync.WaitGroup, echoMode bool) {	
	defer wg.Done()

	for ssrc := range messages {
		// get input files and then use witai to get utterance
		ogg_filename := fmt.Sprintf("userAudio/" + "%d.ogg", ssrc)
		mp3_filename, err := lib.OggToMp3(ogg_filename)
		if err != nil  {
			fmt.Println("mp3 conv err:", err)
		}

		outputText := lib.WitAiCustomPostGetText(mp3_filename)	
		// suppose we got the output here, feed it to sentiment engine and see result
		analysis := sentimentModel.SentimentAnalysis(outputText, sentiment.English)
		fmt.Println("score:", analysis.Score, outputText)
		
		lib.GetMP3ForText(outputText)
		
		stop := make(chan bool)
		if echoMode {
			lib.PlayAudioFile(v, "cache/" +outputText + ".mp3",  stop)
		} else {
			if analysis.Score == 1{
				// play congrats sound
				lib.GetMP3ForText("wow awesome berry good job")
				lib.PlayAudioFile(v, "cache/wow awesome berry good job.mp3",  stop)				
			} else {
				// play oh noes sound
				// play congrats sound
				lib.GetMP3ForText("oh noes")
				lib.PlayAudioFile(v, "cache/oh noes.mp3",  stop)
			}
		}
	}
}

func eventHandler(s *discordgo.Session, m *discordgo.MessageCreate) {
	fmt.Println(m.Author.ID, s.State.User.ID, m.Content, m.ChannelID)
	if m.Content == "<@!" + s.State.User.ID + "> join me" || m.Content == "<@!" + s.State.User.ID + "> repeat after me"{
		guildId := m.GuildID
		channels, err := s.GuildChannels(guildId)//which voice channel the user is on? or just any 1st voice channel of guild
		var channelId string
		for _, channel := range channels {
			if channel.Type == discordgo.ChannelTypeGuildVoice {
				channelId = channel.ID
				break
			}
		}
		if err != nil {
			fmt.Println(err)
		}

		// hacky
		echoMode := m.Content != "<@!" + s.State.User.ID + "> join me"
		go voiceEchoEventLoop(guildId, channelId, echoMode)		
	}
}

func voiceEchoEventLoop(guildId string, channelId string, echoMode bool) {
	v, err := s.ChannelVoiceJoin(guildId, channelId, false, false);
	if err != nil {
		fmt.Println("can't join guild/channel:", err)
		return
	}

	messages := make(chan uint32) // of p.SSRC

	wg := sync.WaitGroup{}
	wg.Add(2)
	go HandleVoiceReceive(v, messages, &wg)
	go HandleBotReply(v, messages, &wg, echoMode)
	wg.Wait()
	v.Close()
	close(v.OpusRecv)
	close(messages)
}

func main() {
	err := godotenv.Load(".env")
	s, err = discordgo.New("Bot "+ os.Getenv("DISCORD_BOT_TOKEN"));
	if err != nil {
		fmt.Println("can't init Discord sesh:", err)
		return
	}
	defer s.Close()

	// configure listener's tracked intents?
	s.AddHandler(eventHandler)
	s.Identify.Intents = discordgo.IntentsGuildVoiceStates | discordgo.IntentsGuildMessages

	err = s.Open()
	if err != nil {
		fmt.Println("can't open conn:", err)
		return
	}

	// init sentiment model
	sentimentModel, err = sentiment.Restore() 
	if err != nil {  
		panic(err) 
	} 

	stop := make(chan os.Signal)
	signal.Notify(stop, os.Interrupt)
	<-stop
	log.Println("Gracefully shutdowning")
}
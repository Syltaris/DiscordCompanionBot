package main

import (
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Syltaris/DiscordCompanionBot/lib"
	"github.com/bwmarrin/discordgo"
	"github.com/cdipaolo/sentiment"
	"github.com/joho/godotenv"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3/pkg/media"
	"github.com/pion/webrtc/v3/pkg/media/oggwriter"
)

const helpText = "You wan sum help? \nType in `@CompanionBot join me` and I will respond to what you say\nType in `@CompanionBot repeat after me` and I will echo your words :)"

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

func UnpackPacketsToFile(rtpChannel chan rtp.Packet, ssrc uint32, messages chan string) {
	var file media.Writer
	var firstWrittenTimestamp, lastWrittenTimestamp int64 = 0, 0
	var filename string

	for {			
		now := time.Now().Unix()
		select {													
		case rtpPacket, _ := <- rtpChannel:
			if firstWrittenTimestamp != 0 && now - firstWrittenTimestamp > 15 { // ! init && speaking too long, so skip to write to file and avoid 20s limit
				break
			}
			
			if file == nil { // indicates a new file to write for interval for
				var err error
				filename = fmt.Sprintf("%d-%d.ogg", rtpPacket.SSRC, now)
				file, err = oggwriter.New(fmt.Sprintf("userAudio/%s", filename), 48000, 2)
				if err != nil {
					fmt.Printf("failed to create file %d.ogg, giving up on recording: %v\n", rtpPacket.SSRC, err)
					return
				}
				
				// this is also when the file is first written
				firstWrittenTimestamp = now
				fmt.Println("write open:", rtpPacket.SSRC, rtpPacket.Timestamp, now)
			}

			lastWrittenTimestamp = now
			err := file.WriteRTP(&rtpPacket)
			if err != nil {
				fmt.Printf("failed to write to file %d.ogg, giving up on recording: %v\n", rtpPacket.SSRC, err)
			}	
		default: // check timeouts
			if now - lastWrittenTimestamp > 1 { //  delay								
				if file == nil { // skip if no file, cuz convo not started
					break
				}
				fmt.Println("write close:", ssrc, lastWrittenTimestamp, now)
				file.Close()
				file = nil
				messages <- filename
				firstWrittenTimestamp = 0
			}
		}
	}
}

func HandleVoiceReceive(v *discordgo.VoiceConnection, messages chan string, wg *sync.WaitGroup, speaking *bool) {
	defer wg.Done()

	c := v.OpusRecv
	rtpChannels := sync.Map{} // make(map[uint32]chan rtp.Packet)

	lastActivityTs := time.Now().Unix()
	
	// receives p packets if incoming
	// should trigger a listen for each start of packet?
	for {
		select {
			case p := <- c: 
				if *speaking ==  true { // warning: speaking should be read only
					break // don't bother listening if bot is talking
				}
				lastActivityTs = time.Now().Unix()
				// process into rtp packets and send to channel
				// if no channel, create 1
				rtpChannel, ok := rtpChannels.Load(p.SSRC)
				if !ok {
					rtpChannel = make(chan rtp.Packet)
					rtpChannels.Store(p.SSRC, rtpChannel)

					go UnpackPacketsToFile(rtpChannel.(chan rtp.Packet), p.SSRC, messages)
				}
				rtpPacket := createPionRTPPacket(p)
				rtpChannel.(chan rtp.Packet) <- *rtpPacket
			default:
				if time.Now().Unix() - lastActivityTs > 60 { // no activity for 1 min
					fmt.Println("closing from inactivity...")
					fetchAndCacheAndPlayMP3(v, "bye bye")
					close(messages)
					return 
				}
			}
		}
}

var positiveReplies = []string {
	"omegalul",
	"that is great",
	"bao chicka wow wow",
	"berry good job",
	"lol",
	"l m eh oh",
	"you are the best",
	"so beautiful",
	"good good",
	"awesome pawsome",
}

var negativeReplies = []string{
	"oh noes",
	"sorry",
	"so sad",
	"better luck next time",
	"oopsies",
	"resident sleeper zz",
	"there there",
	"pe pehands",
}

// horrible, should break this up to simpler logic
// cache elsewhere
func fetchAndCacheAndPlayMP3(v *discordgo.VoiceConnection, text string) {
	stop := make(chan bool)
	filename := "cache/" + text + ".mp3"
	lib.GetMP3ForText(text)
	lib.PlayAudioFile(v, filename,  stop)
}


func HandleBotReply(v *discordgo.VoiceConnection, messages chan string, wg *sync.WaitGroup, echoMode bool, speaking *bool) {	
	defer wg.Done()

	for filename := range messages {
		// get input files and then use witai to get utterance
		*speaking = true
		ogg_filename := fmt.Sprintf("userAudio/" + filename)
		mp3_filename, err := lib.OggToMp3(ogg_filename)
		if err != nil  {
			fmt.Println("mp3 conv err:", err)
		}

		outputText := lib.WitAiCustomPostGetText(mp3_filename)	
		// suppose we got the output here, feed it to sentiment engine and see result
		analysis := sentimentModel.SentimentAnalysis(outputText, sentiment.English)
		fmt.Println("score:", analysis.Score, outputText)

		if outputText == "" { // unknown prediction
			//fetchAndCacheAndPlayMP3(v, "sorry ai do not understand")
			// just skip
		} else {
			if echoMode {
				filename := "cache/" + outputText + ".mp3" // warning: this is broken from fetchandplaymp3
				fetchAndCacheAndPlayMP3(v, outputText)
	
				e := os.Remove(filename)
				if e!= nil {
					fmt.Println("error removing userAudio:",err)
				}
			} else {
				if analysis.Score == 1{
					// play congrats sound
					posText := positiveReplies[rand.Intn( len(positiveReplies))]
					fetchAndCacheAndPlayMP3(v, posText)			
				} else {
					// play oh noes sound
					negText := negativeReplies[rand.Intn( len(negativeReplies))]
					fetchAndCacheAndPlayMP3(v, negText)			
				}
			}
		}


		// cleanup userAudio files once done
		e := os.Remove(ogg_filename)
		if e!= nil {
			fmt.Println("error removing userAudio:",err)
		}
		e = os.Remove(mp3_filename)
		if e!= nil {
			fmt.Println("error removing userAudio:",err)
		}
		*speaking = false

	}
	fmt.Println("messages closed")
}

func eventHandler(s *discordgo.Session, m *discordgo.MessageCreate) {
	if !strings.HasPrefix(m.Content, "<") {
		return
	}
	
	halves := strings.Split(m.Content, ">") 
	uid := strings.TrimFunc(halves[0], func(r rune) bool {
		return !unicode.IsNumber(r)
	})
	command := strings.ToLower(strings.ReplaceAll(halves[1], " ", ""))
	
	fmt.Println(m.Author.ID, s.State.User.ID, m.Content, m.ChannelID)
	
	if s.State.User.ID == uid && command == "joinme" || command == "repeatafterme" || command == "help" {
		if command == "help" {
			s.ChannelMessageSend(m.ChannelID, helpText)
			return 
		}

		guildId := m.GuildID
		channels, err := s.GuildChannels(guildId)//which voice channel the user is on? or just any 1st voice channel of guild
		var channelId string
		for _, channel := range channels {
			if channel.Type == discordgo.ChannelTypeGuildVoice {
				channelId = channel.ID
				break
			}

			// check if bot alr in channel, if so don't open another 
			_, exist := s.VoiceConnections[guildId]
			if exist {
				fmt.Println("error: alr have active voice connection")
				return
			}

		}
		if err != nil {
			fmt.Println(err)
		}

		// hacky
		echoMode := command == "repeatafterme"
		go voiceEchoEventLoop(guildId, channelId, echoMode)		
	}
}


func voiceEchoEventLoop(guildId string, channelId string, echoMode bool) {
	v, err := s.ChannelVoiceJoin(guildId, channelId, false, false);
	if err != nil {
		fmt.Println("can't join guild/channel:", err)
		return
	}
	defer v.Close()

	messages := make(chan string) // of p.SSRC-timestamp
	speaking := false

	wg := sync.WaitGroup{}
	wg.Add(2)
	go HandleVoiceReceive(v, messages, &wg, &speaking)
	go HandleBotReply(v, messages, &wg, echoMode, &speaking)
	wg.Wait()
	fmt.Println("closing voice conn")
	v.Disconnect()
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
	s.Identify.Intents = discordgo.IntentsGuildVoiceStates | discordgo.IntentsGuildMessages 
	s.AddHandler(eventHandler)

	err = s.Open()
	if err != nil {
		fmt.Println("can't open conn:", err)
		return
	}


	err = s.UpdateGameStatus(0, "@CompanionBot help")
	if err != nil {
		fmt.Println("err setting presence", err)
	}

	// init sentiment model
	sentimentModel, err = sentiment.Restore() 
	if err != nil {  
		panic(err) 
	} 

	stop := make(chan os.Signal)
	signal.Notify(stop, os.Interrupt)
	signal.Notify(stop, os.Kill)
	<-stop
	log.Println("Gracefully shutdowning")
}
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

func HandleVoiceReceive(v *discordgo.VoiceConnection, messages chan uint32, wg *sync.WaitGroup, speaking *bool) {
	defer wg.Done()

	c := v.OpusRecv
	files :=  sync.Map{} //make(map[uint32]media.Writer)
	firstWrittenTimestamps := sync.Map{}// make(map[uint32]int64) // used to keep track of too long convos, early term
	lastWrittenTimestamps := sync.Map{} //make(map[uint32]int64) // ssrc : time now unix
	rtpChannels := sync.Map{} // make(map[uint32]chan rtp.Packet)

	lastActivityTs := time.Now().Unix()
	
	// receives p packets if incoming
	// should trigger a listen for each start of packet?
	for {
		select {
			case p := <- c: 
				lastActivityTs = time.Now().Unix()
				// process into rtp packets and send to channel
				// if no channel, create 1
				rtpChannel, ok := rtpChannels.Load(p.SSRC) //rtpChannels[p.SSRC]
				if !ok {
					//rtpChannels[p.SSRC] = make(chan rtp.Packet)
					//rtpChannel = rtpChannels[p.SSRC]

					rtpChannel = make(chan rtp.Packet)
					rtpChannels.Store(p.SSRC, rtpChannel)
					// spawn file processor here? only once when channel is created
					go func(ssrc uint32) {
						for {
							select {													
							case rtpPacket, ok := <- rtpChannel.(chan rtp.Packet):
								// need a way to stop listening while 'speaking'
								if *speaking ==  true { // warning: speaking should be read only
									break 
								}
								now := time.Now().Unix()
								firstTs, ok := firstWrittenTimestamps.Load(rtpPacket.SSRC) //firstWrittenTimestamps[rtpPacket.SSRC]
								if ok && now - firstTs.(int64) > 15 { // speaking too long, skip writing to file to avoid 20s limit
									break
								}
								
								// lastWrittenTimestamps[rtpPacket.SSRC] = now // no mutex...ok?
								lastWrittenTimestamps.Store(rtpPacket.SSRC, now)

								//file, ok := files[rtpPacket.SSRC]
								file, ok := files.Load(rtpPacket.SSRC)
								if !ok { // indicates a new file to write for interval for
									var err error
									file, err = oggwriter.New(fmt.Sprintf("userAudio/%d.ogg", rtpPacket.SSRC), 48000, 2)
									if err != nil {
										fmt.Printf("failed to create file %d.ogg, giving up on recording: %v\n", rtpPacket.SSRC, err)
										return
									}
									//files[rtpPacket.SSRC] = file
									files.Store(rtpPacket.SSRC, file)
									
									// this is also when the file is first written
									//firstWrittenTimestamps[rtpPacket.SSRC] = now
									firstWrittenTimestamps.Store(rtpPacket.SSRC, now)
									fmt.Println("write open:", rtpPacket.SSRC, rtpPacket.Timestamp, now)
								}
								err := file.(media.Writer).WriteRTP(&rtpPacket)
								if err != nil {
									fmt.Printf("failed to write to file %d.ogg, giving up on recording: %v\n", rtpPacket.SSRC, err)
								}	
							default: // check timeouts
								ts, _ := lastWrittenTimestamps.Load(ssrc) //lastWrittenTimestamps[ssrc]
								now := time.Now().Unix()
								if now - ts.(int64) > 1 { //  delay								
									//file, ok := files[ssrc]
									file, ok := files.Load(ssrc)
									if !ok { // skip if no file, cuz convo not started
										break
									}
									fmt.Println("write close:", ssrc, ts, now)
									file.(media.Writer).Close()
									//delete(files, ssrc)
									files.Delete(ssrc)
									messages <- ssrc
									//time.Sleep(3 * time.Second) // prevent writing new file before receiver can process it 
									//lastWrittenTimestamps[ssrc] = time.Now().Add(3 * time.Second).Unix() // give 3s delay to speak
									//delete(firstWrittenTimestamps, ssrc)
									firstWrittenTimestamps.Delete(ssrc)
								}
							}
						}
					}(p.SSRC)
				}
				rtpPacket := createPionRTPPacket(p)
				rtpChannel.(chan rtp.Packet) <- *rtpPacket
			default:
				if time.Now().Unix() - lastActivityTs > 60 { // no activity for 1 min
					fmt.Println("closing from inactivity...")
					lib.GetMP3ForText("bye bye")
					lib.PlayAudioFile(v, "cache/bye bye.mp3", make(<-chan bool))
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


func HandleBotReply(v *discordgo.VoiceConnection, messages chan uint32, wg *sync.WaitGroup, echoMode bool, speaking *bool) {	
	defer wg.Done()

	for ssrc := range messages {
		// get input files and then use witai to get utterance
		*speaking = true
		ogg_filename := fmt.Sprintf("userAudio/" + "%d.ogg", ssrc)
		mp3_filename, err := lib.OggToMp3(ogg_filename)
		if err != nil  {
			fmt.Println("mp3 conv err:", err)
		}

		outputText := lib.WitAiCustomPostGetText(mp3_filename)	
		// suppose we got the output here, feed it to sentiment engine and see result
		analysis := sentimentModel.SentimentAnalysis(outputText, sentiment.English)
		fmt.Println("score:", analysis.Score, outputText)

		if outputText == "" { // unknown prediction
			fetchAndCacheAndPlayMP3(v, "sorry ai do not understand")
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

	messages := make(chan uint32) // of p.SSRC
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
	<-stop
	log.Println("Gracefully shutdowning")
}
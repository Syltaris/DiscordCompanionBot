package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"
	"layeh.com/gopus"

	"github.com/cdipaolo/sentiment"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3/pkg/media"
	"github.com/pion/webrtc/v3/pkg/media/oggwriter"
	witai "github.com/wit-ai/wit-go"
)

var witAiClient *witai.Client

var guildId = "829599334127501312"
var channelId = "829599334127501316"

var witAiToken = ""
var botToken = ""

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

// OnError gets called by dgvoice when an error is encountered.
// By default logs to STDERR
var OnError = func(str string, err error) {
	prefix := "dgVoice: " + str

	if err != nil {
		os.Stderr.WriteString(prefix + ": " + err.Error())
	} else {
		os.Stderr.WriteString(prefix)
	}
}

// Technically the below settings can be adjusted however that poses
// a lot of other problems that are not handled well at this time.
// These below values seem to provide the best overall performance
const (
	channels  int = 2                   // 1 for mono, 2 for stereo
	frameRate int = 48000               // audio sampling rate
	frameSize int = 960                 // uint16 size of each audio frame
	maxBytes  int = (frameSize * 2) * 2 // max size of opus data
)
var (
	speakers    map[uint32]*gopus.Decoder
	opusEncoder *gopus.Encoder
	mu          sync.Mutex
)
// SendPCM will receive on the provied channel encode
// received PCM data into Opus then send that to Discordgo
func SendPCM(v *discordgo.VoiceConnection, pcm <-chan []int16) {
	if pcm == nil {
		return
	}

	var err error

	opusEncoder, err = gopus.NewEncoder(frameRate, channels, gopus.Audio)

	if err != nil {
		OnError("NewEncoder Error", err)
		return
	}

	for {

		// read pcm from chan, exit if channel is closed.
		recv, ok := <-pcm
		if !ok {
			OnError("PCM Channel closed", nil)
			return
		}

		// try encoding pcm frame with Opus
		opus, err := opusEncoder.Encode(recv, frameSize, maxBytes)
		if err != nil {
			OnError("Encoding Error", err)
			return
		}

		if v.Ready == false || v.OpusSend == nil {
			// OnError(fmt.Sprintf("Discordgo not ready for opus packets. %+v : %+v", v.Ready, v.OpusSend), nil)
			// Sending errors here might not be suited
			return
		}
		// send encoded opus data to the sendOpus channel
		v.OpusSend <- opus
	}
}
// PlayAudioFile will play the given filename to the already connected
// Discord voice server/channel.  voice websocket and udp socket
// must already be setup before this will work.
func PlayAudioFile(v *discordgo.VoiceConnection, filename string, stop <-chan bool) {

	// Create a shell command "object" to run.
	run := exec.Command("ffmpeg", "-i", filename, "-f", "s16le", "-ar", strconv.Itoa(frameRate), "-ac", strconv.Itoa(channels), "pipe:1")
	ffmpegout, err := run.StdoutPipe()
	if err != nil {
		OnError("StdoutPipe Error", err)
		return
	}

	ffmpegbuf := bufio.NewReaderSize(ffmpegout, 16384)

	// Starts the ffmpeg command
	err = run.Start()
	if err != nil {
		OnError("RunStart Error", err)
		return
	}

	// prevent memory leak from residual ffmpeg streams
	defer run.Process.Kill()

	//when stop is sent, kill ffmpeg
	go func() {
		<-stop
		err = run.Process.Kill()
	}()

	// Send "speaking" packet over the voice websocket
	err = v.Speaking(true)
	if err != nil {
		OnError("Couldn't set speaking", err)
	}

	// Send not "speaking" packet over the websocket when we finish
	defer func() {
		err := v.Speaking(false)
		if err != nil {
			OnError("Couldn't stop speaking", err)
		}
	}()

	send := make(chan []int16, 2)
	defer close(send)

	close := make(chan bool)
	go func() {
		SendPCM(v, send)
		close <- true
	}()

	for {
		// read data from ffmpeg stdout
		audiobuf := make([]int16, frameSize*channels)
		err = binary.Read(ffmpegbuf, binary.LittleEndian, &audiobuf)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return
		}
		if err != nil {
			OnError("error reading from ffmpeg stdout", err)
			return
		}

		// Send received PCM to the sendPCM channel
		select {
		case send <- audiobuf:
		case <-close:
			return
		}
	}
}

// with correct text
type MessageResponse struct {
	ID       string                 `json:"msg_id"`
	Text     string                 `json:"text"`
	Entities map[string]interface{} `json:"entities"`
}

func handleVoice(v *discordgo.VoiceConnection, messages chan uint32, wg *sync.WaitGroup) {
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

func customWitAiPostGetText(filename string) string {
	file, err := os.ReadFile(filename)
	if err != nil {
		fmt.Println("can't open file:" ,err)
	}
	body := bytes.NewBuffer(file)
	req, err := http.NewRequest("POST", "https://api.wit.ai/speech?q=", body)
	if err != nil {
		fmt.Println(err)
	}
	
	headerAuth := fmt.Sprintf("Bearer %s", witAiToken)
	headerAccept := fmt.Sprintf("application/vnd.wit.%s+json", "20170307")
	contentType := "audio/mpeg3"
	
	req.Header.Set("Authorization", headerAuth)
	req.Header.Set("Accept", headerAccept)
	req.Header.Set("Content-Type", contentType)

	httpClient := &http.Client{
		Timeout: time.Second * 10,
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		fmt.Println(err)
	}

	if resp.StatusCode >= http.StatusBadRequest {
		defer resp.Body.Close()

		// var e *errorResp
		// decoder := json.NewDecoder(resp.Body)
		// err = decoder.Decode(&e)
		// if err != nil {
		// 	fmt.Println(fmt.Errorf("unable to decode error message: %s", err.Error()))
		// }

		// // Wit.ai errors sometimes have "error", sometimes "body" message
		// if len(e.Error) > 0 {
		// 	fmt.Println(fmt.Errorf("unable to make a request. error: %s", e.Error))
		// }

		// if len(e.Body) > 0 {
		// 	fmt.Println(fmt.Errorf("unable to make a request. error: %s", e.Body))
		// }
	}

	bytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Fatalln(err)
	}

	text := string(bytes)
	fmt.Println(text)
	var msgResponse MessageResponse
	json.Unmarshal([]byte(text), &msgResponse)
	return msgResponse.Text
}

// NOTE: wit.ai doesn't support stereo sound for now
// (https://wit.ai/docs/http/20160516#post--speech-link)
func oggToMp3(oggFilepath string) (mp3Filepath string, err error) {
	mp3Filepath = fmt.Sprintf("%s.mp3", oggFilepath)

	// $ ffmpeg -i input.ogg -ac 1 output.mp3
	params := []string{"-i", oggFilepath,"-y", mp3Filepath}
	cmd := exec.Command("ffmpeg", params...)

	if _, err = cmd.CombinedOutput(); err != nil {
		mp3Filepath = ""
	}

	return mp3Filepath, err
}

func botResponse(v *discordgo.VoiceConnection, messages chan uint32, wg *sync.WaitGroup) {	
	defer wg.Done()

	for ssrc := range messages {
		// get input files and then use witai to get utterance
		fmt.Println(ssrc)
		ogg_filename := fmt.Sprintf("%d.ogg", ssrc)
		mp3_filename, err := oggToMp3(ogg_filename)
		if err != nil  {
			fmt.Println("mp3 conv err:", err)
		}
		//mp3_filename := fmt.Sprintf("%d.mp3", ssrc)

		output := customWitAiPostGetText(mp3_filename)
		fmt.Println("resp:", output)
		// file, err := os.Open(fmt.Sprintf("%d.ogg", ssrc))
		// if err != nil {
		// 	fmt.Println("can't open file:" ,err)
		// }
		// // send to wit AI to parse
		// speech := witai.Speech{File: file, ContentType: "audio/ogg"}
		// msg, err := witAiClient.Speech(&witai.MessageRequest{Speech: &speech})
		// if err != nil {
		// 	fmt.Println("can't send to witAi:", err)
		// }
		// fmt.Println("output: ",msg.Text, msg)
	
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
		
		getVoiceMP3(outputText)
	
		if analysis.Score == 1{
			// play congrats sound
			
		} else {
			// play oh noes sound
		}
		stop := make(chan bool)
		PlayAudioFile(v, outputText + ".mp3",  stop)
	}
}

func getVoiceMP3(text string) {
	// if voice file exists, don't need to fetch again
	if _, err := os.Stat(text + ".mp3"); os.IsNotExist(err) {
		fmt.Println("file not in cache, proceed to fetch")
		bytes, err := getVoiceForText(text)
		// try to save as mp3 see how to process data
		f, err := os.Create(text + ".mp3")
		if err != nil {
			fmt.Println("can't create file: ", err)
			return
		}
		defer f.Close()
		f.Write(bytes)
	}
	fmt.Println("seems exist or created")
	return
}

func getVoiceForText(text string) ([]byte, error) {
	url := fmt.Sprintf("https://texttospeech.responsivevoice.org/v1/text:synthesize?text=%s&lang=vi&engine=g1&name=&pitch=0.5&rate=0.5&volume=1&key=WfWmvaX0&gender=female", strings.Replace(text, " ", "+", -1 ))
	resp, err := http.Get(url)
	if err != nil {
		fmt.Println("fetch err:", err)
		return nil, err
	}
	defer resp.Body.Close()
	
	bytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Fatalln(err)
	}	
	return bytes, nil
}

func main() {
	err := godotenv.Load(".env")
	botToken = os.Getenv("DISCORD_BOT_TOKEN")
	witAiToken = os.Getenv("WIT_AI_TOKEN")

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

	//defer v.Close()
	//defer close(v.OpusRecv)

	messages := make(chan uint32) // of p.SSRC

	wg := sync.WaitGroup{}
	wg.Add(2)
	go handleVoice(v, messages, &wg)
	go botResponse(v, messages, &wg)

	wg.Wait()
}
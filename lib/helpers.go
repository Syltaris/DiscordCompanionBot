package lib

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/bwmarrin/discordgo"
	"gopkg.in/hraban/opus.v2"
)

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
	sampleRate int = 48000               // audio sampling rate
	frameSize int = 960                 // uint16 size of each audio frame
	maxBytes  int = (frameSize * 2) * 2 // max size of opus data
)
// SendPCM will receive on the provied channel encode
// received PCM data into Opus then send that to Discordgo
func SendPCM(v *discordgo.VoiceConnection, pcm <-chan []int16) {
	if pcm == nil {
		return
	}
	var err error

	opusEncoder, err := opus.NewEncoder(sampleRate, channels, opus.AppVoIP)
	if err != nil {
		OnError("NewEncoder Error", err)
		return
	}
	for {
		// read pcm from chan, exit if channel is closed.
		recv, ok := <-pcm
		if !ok {
			OnError("PCM Channel closed\n", nil)
			return
		}

		// frameSize := len(pcm) // must be interleaved if stereo
		// frameSizeMs := float32(frameSize) / float32(channels) * 1000.0 / float32(sampleRate)
		// switch frameSizeMs {
		// case 2.5, 5, 10, 20, 40, 60:
		// 	// Good.
		// default:
		// 	fmt.Errorf("Illegal frame size: %d bytes (%f ms)", frameSize, frameSizeMs)
		// 	return 
		// }
		// try encoding pcm frame with Opus
		buf := make([]byte, maxBytes)
		n, err := opusEncoder.Encode(recv, buf)
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
		v.OpusSend <- buf[:n]
	}
}
// PlayAudioFile will play the given filename to the already connected
// Discord voice server/channel.  voice websocket and udp socket
// must already be setup before this will work.
func PlayAudioFile(v *discordgo.VoiceConnection, filename string, stop <-chan bool) {

	// Create a shell command "object" to run.
	run := exec.Command("ffmpeg", "-i", filename, "-f", "s16le", "-ar", strconv.Itoa(sampleRate), "-ac", strconv.Itoa(channels), "pipe:1")
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

// NOTE: wit.ai doesn't support stereo sound for now
// (https://wit.ai/docs/http/20160516#post--speech-link)
func OggToMp3(oggFilepath string) (mp3Filepath string, err error) {
	mp3Filepath = fmt.Sprintf("%s.mp3", oggFilepath)

	// $ ffmpeg -i input.ogg -ac 1 output.mp3
	params := []string{"-i", oggFilepath,"-y", mp3Filepath}
	cmd := exec.Command("ffmpeg", params...)

	if _, err = cmd.CombinedOutput(); err != nil {
		mp3Filepath = ""
	}

	return mp3Filepath, err
}

// with correct text
type MessageResponse struct {
	ID       string                 `json:"msg_id"`
	Text     string                 `json:"text"`
	Entities map[string]interface{} `json:"entities"`
}

func WitAiCustomPostGetText( filename string) string {
	file, err := os.ReadFile(filename)
	if err != nil {
		fmt.Println("can't open file:" ,err)
	}
	body := bytes.NewBuffer(file)
	req, err := http.NewRequest("POST", "https://api.wit.ai/speech?q=", body)
	if err != nil {
		fmt.Println(err)
	}
	
	witAiToken := os.Getenv("WIT_AI_TOKEN")

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
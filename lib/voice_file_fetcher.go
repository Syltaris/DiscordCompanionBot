package lib

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
)

const responsiveVoiceUriTemplate = "https://texttospeech.responsivevoice.org/v1/text:synthesize?text=%s&lang=vi&engine=g1&name=&pitch=0.5&rate=0.5&volume=1&key=WfWmvaX0&gender=female"

func GetMP3ForText(text string) {
	// if voice file exists, don't need to fetch again
	filename := "cache/"+text + ".mp3"
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		fmt.Println("file not in cache, proceed to fetch")
		bytes, err := getVoiceForText(text)
		// try to save as mp3 see how to process data
		f, err := os.Create(filename)
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
	url := fmt.Sprintf(responsiveVoiceUriTemplate, strings.Replace(text, " ", "+", -1 ))
	resp, err := http.Get(url)
	if err != nil {
		fmt.Println("fetch err:", err)
		return nil, err
	}
	defer resp.Body.Close()
	
	bytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Println(err)
	}	
	return bytes, nil
}
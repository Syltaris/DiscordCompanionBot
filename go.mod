module github.com/Syltaris/DiscordCompanionBot

go 1.16

require (
	github.com/Syltaris/DiscordCompanionBot/lib v0.0.0
	github.com/bwmarrin/discordgo v0.23.2
	github.com/cdipaolo/goml v0.0.0-20190412180403-e1f51f713598 // indirect
	github.com/cdipaolo/sentiment v0.0.0-20200617002423-c697f64e7f10
	github.com/joho/godotenv v1.3.0
	github.com/pion/rtp v1.6.2
	github.com/pion/webrtc/v3 v3.0.20
	golang.org/x/sys v0.0.0-20210403161142-5e06dd20ab57 // indirect
	golang.org/x/text v0.3.6 // indirect
	gopkg.in/hraban/opus.v2 v2.0.0-20201025103112-d779bb1cc5a2 // indirect
)

replace github.com/Syltaris/DiscordCompanionBot/lib => ./lib

// +heroku goVersion go1.16

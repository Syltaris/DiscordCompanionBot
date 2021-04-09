# FROM golang:alpine AS build
# WORKDIR /src/
# COPY . /src/

# # Install git.
# # Git is required for fetching the dependencies.
# RUN apk update && apk add --no-cache git
# RUN apk add build-base
# RUN apk add  --no-cache ffmpeg

# RUN CGO_ENABLED=0 GOOS=linux go build -tags nolibopusfile -o dcb . 

# FROM scratch
# COPY --from=build /src/dcb /dcb
# #ENTRYPOINT ["./bin/app"]
# CMD ["./dcb"]

# RUN mkdir -p /go/src/app
# WORKDIR /go/src/app

# CMD ["go-wrapper", "run"]
# COPY . /go/src/app
# RUN go-wrapper download
# RUN go-wrapper install

# Choose any golang image, just make sure it doesn't have -onbuild
FROM golang:1.16

RUN apt-get update && apt-get -y install libopus-dev libopusfile-dev ffmpeg

# Everything below is copied manually from the official -onbuild image,
# with the ONBUILD keywords removed.

RUN mkdir -p /go/src/app
WORKDIR /go/src/app

COPY . /go/src/app
RUN go get
RUN go  install
CMD ["go", "run", "main.go"]
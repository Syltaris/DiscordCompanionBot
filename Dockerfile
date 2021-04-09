FROM golang:alpine AS build
WORKDIR /src/
COPY . /src/

# Install git.
# Git is required for fetching the dependencies.
RUN apk update && apk add --no-cache git
RUN apk add build-base
RUN apk add  --no-cache ffmpeg


RUN CGO_ENABLED=1 GOOS=linux go build -o dcb .

FROM scratch
COPY --from=build /dcb /dcb
#ENTRYPOINT ["./bin/app"]
CMD ["/dcb"]
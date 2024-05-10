FROM golang:1.18.2-alpine3.15

COPY . /go/src/sensenova-proxy-for-openai
WORKDIR /go/src/sensenova-proxy-for-openai
RUN GOOS=linux GOARCH=amd64 go build -o main main.go

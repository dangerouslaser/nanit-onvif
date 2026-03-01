FROM golang:1.26 AS build
ADD . /app/
WORKDIR /app
RUN go mod tidy
RUN CGO_ENABLED=0 go build -ldflags "-X main.GitCommit=$(git rev-parse --short HEAD)" -o ./bin/nanit ./cmd/nanit/*.go

FROM alpine
RUN mkdir -p /app/data
COPY --from=build /app/bin/nanit /app/bin/nanit
WORKDIR /app
EXPOSE 8554
EXPOSE 8089
VOLUME [ "/app/data" ]
CMD ["/app/bin/nanit"]
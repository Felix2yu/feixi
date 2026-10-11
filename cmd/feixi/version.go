package main

// version 由 CI 注入：-ldflags "-X main.version=v1.2.3"；本地 go build 时为 "dev"。
var version = "dev"

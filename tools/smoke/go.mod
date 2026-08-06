module github.com/biumind/biumind/tools/smoke

go 1.25.7

require github.com/biumind/biumind/packages/go-sdk/biu v0.0.0

require (
	github.com/klauspost/compress v1.18.5 // indirect
	github.com/nats-io/nats.go v1.52.0 // indirect
	github.com/nats-io/nkeys v0.4.15 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	golang.org/x/crypto v0.50.0 // indirect
	golang.org/x/sys v0.43.0 // indirect
)

replace github.com/biumind/biumind/packages/go-sdk/biu => ../../packages/go-sdk/biu

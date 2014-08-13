all: test-image

dockerapp: dockerapp.go
	GOARCH=amd64 GOOS=linux go build -o dockerapp dockerapp.go

test-image: dockerapp
	docker build -t cloudfoundry/inigodockertest .

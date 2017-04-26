package main

import (
	"github.com/fsouza/go-dockerclient"
	"log"
	"os"
	"text/template"
	"github.com/go-redis/redis"
	"strconv"
	"github.com/progrium/go-shell"
	"runtime"
	"strings"
	"time"
)

func contains(slice []string, item string) bool {
	set := make(map[string]struct{}, len(slice))
	for _, s := range slice {
		set[s] = struct{}{}
	}

	_, ok := set[item]
	return ok
}

func main() {
	const nginx = `
server {
	listen 80 default_server;
	server_name _; # This is just an invalid value which will never trigger on a real hostname.
	error_log /proc/self/fd/2;
	access_log /proc/self/fd/1;
	return 503;
}

upstream {{ .Name }} {
{{ range $key, $port := .Ports }}
    server 127.0.0.1:{{ $port }};
{{ end }}
    keepalive 16;
}

server {
	gzip_types text/plain text/css application/json application/x-javascript text/xml application/xml application/xml+rss text/javascript;

	server_name {{ .Name }};
	proxy_buffering off;
	error_log /proc/self/fd/2;
	access_log /proc/self/fd/1;

	location / {
		proxy_pass http://{{ .Name }};
		proxy_set_header Host $http_host;
		proxy_set_header X-Real-IP $remote_addr;
		proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
		proxy_set_header X-Forwarded-Proto $scheme;

		# HTTP 1.1 support
		proxy_http_version 1.1;
		proxy_set_header Connection "";
	}
}
`
	endpoint := "unix:///var/run/docker.sock"
	dockerClient, err := docker.NewClient(endpoint)
	if err != nil {
		panic(err)
	}

	type DomainHost struct {
		Name string
		Ports []string
	}


	redisClient := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "", // no password set
		DB:       0,  // use default DB
	})
	redisClient.Ping()





	log.Println("current OS is ", runtime.GOOS)


	containers, err := dockerClient.ListContainers(docker.ListContainersOptions{All: false})
	if err != nil {
		panic(err)
	}


	lRange := redisClient.LRange("ports", 0, -1)
	if lRange.Err() != nil {
		panic(lRange.Err())
	}

	runningPorts := make([]string, 0)
	for _, springPort := range lRange.Val()  {
		p, err := strconv.Atoi(springPort)
		if err != nil {
			panic(err)
		}
		if p > 0 {
			runningPorts = append(runningPorts, springPort)
		}
	}


	var h = DomainHost{Name: "www.lipuwater.com", Ports: runningPorts}
	t := template.Must(template.New("nginx").Parse(nginx))

	f, err := os.Create("/tmp/nginx.conf")
	if err != nil {
		panic(err)
	}
	defer f.Close()

	err = t.Execute(f, h)
	if err != nil {
		panic(err)
	}

	// update nginx conf and reload
	// /etc/nginx/sites
	shell.Cmd("sudo", "cp -f /tmp/nginx.conf /tmp/www.lipuwater.com.conf").Run()
	shell.Cmd("sudo", "nginx -s reload").Run()


	// stop none use container
	time.Sleep(100)
	for _, container := range containers  {
		log.Println(container.ID + " " + container.Image)
		for _, containerPort := range container.Ports  {
			if containerPort.PrivatePort == containerPort.PublicPort {
				if containerPort.PrivatePort > 8999 && containerPort.PrivatePort < 10000 {
					p := strconv.FormatInt(containerPort.PrivatePort, 10)
					if !contains(runningPorts, p) {
						// stop this container
						log.Println("The container ", container.ID, " will be stop")

						var connectionCount = 0
						var cmdOut *shell.Process

						if runtime.GOOS == "darwin" {
							// sudo netstat -antl -p tcp | grep -e ESTABLISHED -e TIME_WAIT -e CLOSE_WAIT -e SYN_SENT | grep 127.0.0.1:8002
							cmdOut = shell.Cmd("sudo", "netstat -antl -p tcp").Pipe("grep", "-e ESTABLISHED -e TIME_WAIT -e CLOSE_WAIT -e SYN_SEN").Pipe("wc", "-l").Run()
						} else if runtime.GOOS == "linux" {
							cmdOut = shell.Cmd("sudo", "netstat -antp").Pipe("grep", "-e ESTABLISHED -e TIME_WAIT -e CLOSE_WAIT -e SYN_SEN").Pipe("wc", "-l").Run()
						} else {
							panic("No support for this OS")
						}

						connectionCount, err = strconv.Atoi(strings.TrimSpace(cmdOut.String()))
						if err != nil {
							panic(err)
						}

						log.Println("connectionCount is ", connectionCount)
						if connectionCount > 0 {
							dockerClient.StopContainer(container.ID, 100) // given timeout (in seconds)
						}
					}
				}
			}
		}
	}
}
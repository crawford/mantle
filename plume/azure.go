/*
   Copyright 2014 CoreOS, Inc.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package main

import (
	"compress/bzip2"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/coreos/mantle/auth"
	"github.com/coreos/mantle/cli"

	"github.com/coreos/mantle/Godeps/_workspace/src/code.google.com/p/google-api-go-client/storage/v1"
)

var cmdAzure = &cli.Command{
	Name:        "azure",
	Description: "Publish Azure image to Azure Storage and mark as public",
	Summary:     "Publish Azure image",
	Usage:       "gs://bucket/prefix/ [gs://...]",
	Run:         runAzure,
}

const (
	buildsBucket                = "builds.release.core-os.net"
	azureProductionObjectFormat = "%s/boards/amd64-usr/%s/coreos_production_azure_image.vhd.bz2"
)

func init() {
	cli.Register(cmdAzure)
}

func runAzure(args []string) int {
	if len(args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: azure <channel> <version>\n")
		return 2
	}
	channel := strings.ToLower(args[0])
	version := args[1]

	file, err := os.OpenFile("azure.vhd", os.O_WRONLY|os.O_CREATE, os.FileMode(0644))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed opening file: %v\n", err)
		return 1
	}

	if err := downloadImage(channel, version, file); err != nil {
		fmt.Fprintf(os.Stderr, "Failed downloading image: %v\n", err)
		return 1
	}
	file.Close()

	blobUrl := fmt.Sprintf("https://coreos.blob.core.windows.net/crawford/coreos-%s-%s.vhd", version, channel)
	label := fmt.Sprintf("CoreOS %s", strings.Title(channel))
	description := "The beta channel consists of promoted alpha releases. Mix a few beta machines into your production clusters to catch any bugs specific to your hardware or configuration."
	name := "CoreOS-522.5.0-stable"
	filename := "azure.vhd"

	cmd := exec.Command("azure", "vm", "image", "create", `--os=linux`,
		fmt.Sprintf(`--blob-url=%s`, blobUrl),
		fmt.Sprintf(`--label="%s"`, label),
		fmt.Sprintf(`--description="%s"`, description),
		name,
		filename)
	if output, err := cmd.CombinedOutput(); err == nil {
		fmt.Print(string(output))
	} else {
		fmt.Print(string(output))
		fmt.Fprintf(os.Stderr, "Failed creating azure image: %v\n", err)
	}

	return 0
}

func downloadImage(channel, version string, file *os.File) error {
	client, err := auth.GoogleClient(false)
	if err != nil {
		return fmt.Errorf("download image: %v", err)
	}

	url, err := fetchMediaLink(client, channel, version)
	if err != nil {
		return fmt.Errorf("download image: %v", err)
	}

	resp, err := client.Do(&http.Request{Method: "GET", URL: url})
	if err != nil {
		return fmt.Errorf("download image: %v", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case 200:
	default:
		return fmt.Errorf("download image: %v", resp.Status)
	}

	io.Copy(file, bzip2.NewReader(resp.Body))

	return nil
}

func fetchMediaLink(client *http.Client, channel, version string) (*url.URL, error) {
	obj, err := fetchImageObject(client, channel, version)
	if err != nil {
		return nil, fmt.Errorf("fetch media link: %v", err)
	}

	url, err := parseMediaLink(obj)
	if err != nil {
		return nil, fmt.Errorf("fetch media link: %v", err)
	}

	return url, nil
}

func fetchImageObject(client *http.Client, channel, version string) (*storage.Object, error) {
	service, err := storage.New(client)
	if err != nil {
		return nil, fmt.Errorf("fetch image object: %v", err)
	}

	name := fmt.Sprintf(azureProductionObjectFormat, channel, version)
	object, err := service.Objects.Get(buildsBucket, name).Do()
	if err != nil {
		return nil, fmt.Errorf("fetch image object: %v", err)
	}

	return object, nil
}

func parseMediaLink(object *storage.Object) (*url.URL, error) {
	u, err := url.Parse(object.MediaLink)
	if err != nil {
		return nil, fmt.Errorf("parse media link: %v", err)
	}

	return &url.URL{
		Scheme: u.Scheme,
		Host:   u.Host,
		Opaque: strings.TrimPrefix(object.MediaLink, u.Scheme+":"),
	}, nil
}

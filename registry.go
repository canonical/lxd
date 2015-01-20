package lxd

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
)

type imageServer struct {
	Name          string              `json:"name"`
	Desc          string              `json:"description"`
	DescFr        string              `json:"description.fr"`
	Url           string              `json:"url"`
	Arguments     []map[string]string `json:"arguments"`
	TrustedKeys   []string            `json:"trusted_keys"`
	TrustedCerts  []string            `json:"trusted_certs"`
	MinClientVer  int                 `json:"min_client_ver"`
	MaxClientVer  int                 `json:"max_client_ver"`
	imagePathList map[string]string
}

type registryServer struct {
	Version         int           `json:"version"`
	GenAt           uint64        `json:"generated_at"`
	ImageServerList []imageServer `json:"servers"`
}

type RegistryManager struct {
	serverCount int
	client      *http.Client
	registry    registryServer
}

func (rm *RegistryManager) InitRegistryManager() {
	proxyUrl, err := url.Parse("http://172.26.0.75:8080")
	if err != nil {
		fmt.Println(err)
	}

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		Proxy:           http.ProxyURL(proxyUrl),
	}
	rm.client = &http.Client{Transport: tr}

}

func (rm *RegistryManager) FetchImageServerData() error {

	resp, err := rm.client.Get("https://registry.linuxcontainers.org/1.0/index.json")
	if err != nil {
		fmt.Println(err)
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)

	servers := make([]imageServer, 1)
	keyList := make([]string, 0)
	servers[0].TrustedKeys = keyList
	rm.registry.ImageServerList = servers

	err = json.Unmarshal(body, &rm.registry)
	if err != nil {
		fmt.Println(err)
	}
	return err

}

func (rm *RegistryManager) GetImageServers() ([]string, error) {

	urlList := make([]string, 0)
	var err error

	for i := range rm.registry.ImageServerList {
		urlList = append(urlList, rm.registry.ImageServerList[i].Name)
	}
	return urlList, err
}

func (rm *RegistryManager) getImageServerUrl(server_name string) (string, int, error) {

	var url string
	var err error
	var i int

	for i = range rm.registry.ImageServerList {
		if server_name == rm.registry.ImageServerList[i].Name {
			url, _ = getURL(rm.registry.ImageServerList[i].Url)
		}

	}
	if url == "" {
		err = errors.New("No such server found in list")
	}
	return url, i, err
}

func (rm *RegistryManager) GetImageList(image_server string) ([]string, error) {

	image_server_list := make(map[string]string)
	image_list := make([]string, 0)
	url, index, err := rm.getImageServerUrl(image_server)
	if err != nil {
		fmt.Println(err)
		return nil, err
	}
	resp, err := rm.client.Get(url + "/meta/1.0/index-system")
	if err != nil {
		fmt.Println(err)
		return nil, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	reader := bytes.NewReader(body)

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		tokens := getTokens(scanner.Text(), ";", 6)
		image_server_list[tokens[0]+"/"+tokens[1]+"/"+tokens[2]] = tokens[5]
		image_list = append(image_list, tokens[0]+"/"+tokens[1]+"/"+tokens[2])

	}
	if err := scanner.Err(); err != nil {
		fmt.Println(err)
	}
	rm.registry.ImageServerList[index].imagePathList = image_server_list
	return image_list, err

}

func (rm *RegistryManager) GetImageServerPath(image_name string) (string, error) {
	image_server := getTokens(image_name, "/", 2)
	_, index, err := rm.getImageServerUrl(image_server[0])
	if err != nil {
		fmt.Println(err)
		return "", err
	}

	fmt.Println(rm.registry.ImageServerList[index].imagePathList[image_server[1]])
	return rm.registry.ImageServerList[index].imagePathList[image_server[1]], err
}

func getURL(link string) (string, error) {
	var err error
	proto := strings.SplitN(link, "+", 2)
	if proto[1] == "" {
		err = errors.New("URL is not as expected")
	}
	uri := strings.SplitN(link, ":", 2)
	return proto[0] + ":" + uri[1], err
}

func getTokens(uri string, token string, count int) []string {
	result := strings.SplitN(uri, token, count)
	return result
}

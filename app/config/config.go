package config

import (
	"encoding/json"
	"os"
	"sync"

	"github.com/spf13/viper"
)

var (
	C    = new(Config)
	once sync.Once
)

func MustLoad(path string) {
	once.Do(func() {
		viper.SetConfigFile(path)
		err := viper.ReadInConfig()
		if err != nil {
			panic(err)
		}
		err = viper.Unmarshal(&C)
		if err != nil {
			panic(err)
		}

	})
}

func PrintWithJSON() {
	b, err := json.MarshalIndent(C, "", " ")
	if err != nil {
		os.Stdout.WriteString("[CONFIG] JSON marshal error: " + err.Error())
		return
	}
	os.Stdout.WriteString(string(b) + "\n")
}

type ntpConfig struct {
	Server   string `yaml:"server" json:"server"`
	Allow    string `yaml:"allow" json:"allow"`
	Timezone string `yaml:"timezone" json:"timezone"`
}

type nfsConfig struct {
	Server string `yaml:"server" json:"server"`
	Path   string `yaml:"path" json:"path"`
}

type nodeConfig struct {
	Address  string   `yaml:"address" json:"address"`
	Hostname string   `yaml:"hostname" json:"hostname"`
	Role     []string `yaml:"role" json:"role"`
	Port     uint16   `yaml:"port" json:"port"`
	Username string   `yaml:"username" json:"username"`
	Password string   `yaml:"password" json:"password"`
}

type Config struct {
	Namespace string        `yaml:"namespace" json:"namespace"`
	Registry  string        `yaml:"registry" json:"registry"`
	CRISocket string        `yaml:"cri-socket" json:"cri-socket"`
	Vip       string        `yaml:"vip" json:"vip"`
	NTP       ntpConfig     `yaml:"ntp" json:"ntp"`
	NFS       nfsConfig     `yaml:"nfs" json:"nfs"`
	Nodes     []*nodeConfig `yaml:"nodes" json:"nodes"`
}

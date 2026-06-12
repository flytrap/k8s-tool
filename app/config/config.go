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
	Server   string `mapstructure:"server" yaml:"server" json:"server"`
	Allow    string `mapstructure:"allow" yaml:"allow" json:"allow"`
	Timezone string `mapstructure:"timezone" yaml:"timezone" json:"timezone"`
}

type nfsConfig struct {
	Server string `mapstructure:"server" yaml:"server" json:"server"`
	Path   string `mapstructure:"path" yaml:"path" json:"path"`
}

type nodeConfig struct {
	Address  string   `mapstructure:"address" yaml:"address" json:"address"`
	Hostname string   `mapstructure:"hostname" yaml:"hostname" json:"hostname"`
	Role     []string `mapstructure:"role" yaml:"role" json:"role"`
	Port     uint16   `mapstructure:"port" yaml:"port" json:"port"`
	Username string   `mapstructure:"username" yaml:"username" json:"username"`
	Password string   `mapstructure:"password" yaml:"password" json:"password"`
	KeyPath  string   `mapstructure:"keyPath" yaml:"keyPath" json:"keyPath"`
}

type Config struct {
	Namespace string        `mapstructure:"namespace" yaml:"namespace" json:"namespace"`
	Registry  string        `mapstructure:"registry" yaml:"registry" json:"registry"`
	CRISocket string        `mapstructure:"cri-socket" yaml:"cri-socket" json:"cri-socket"`
	Vip       string        `mapstructure:"vip" yaml:"vip" json:"vip"`
	NTP       ntpConfig     `mapstructure:"ntp" yaml:"ntp" json:"ntp"`
	NFS       nfsConfig     `mapstructure:"nfs" yaml:"nfs" json:"nfs"`
	Nodes     []*nodeConfig `mapstructure:"nodes" yaml:"nodes" json:"nodes"`
}

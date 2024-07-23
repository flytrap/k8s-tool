package engine

import (
	"fmt"
	"strconv"
	"strings"
)

type Step struct {
	Num   int
	Name  string
	Steps []*Step
	run   func(*Engine) error
}

func (s *Step) install(e *Engine, n ...int) error {
	var b strings.Builder
	for _, v := range n {
		b.WriteString(strconv.Itoa(v))
		b.WriteString(".")
	}
	b.WriteString(strconv.Itoa(s.Num))
	if e.OnNextStep != nil {
		e.OnNextStep(s.Name)
	} else {
		fmt.Printf("%s) %s\n", b.String(), s.Name)
	}
	if s.run != nil {
		if err := s.run(e); err != nil {
			return err
		}
	}

	n = append(n, s.Num)
	for i := range s.Steps {
		if err := s.Steps[i+1].install(e, n...); err != nil {
			return err
		}
	}
	return nil
}

var DeploySteps = []*Step{
	{Num: 1, Name: "connect", run: func(e *Engine) error { return e.connect() }},
	{Num: 2, Name: "init", run: func(e *Engine) error { return e.init() }},
	{Num: 3, Name: "install chrony", run: func(e *Engine) error { return e.installChrony() }},
	{Num: 4, Name: "install docker", run: func(e *Engine) error { return e.installDocker() }},
	{Num: 5, Name: "load docker image", run: func(e *Engine) error { return e.loadDockerImage() }},
	{Num: 6, Name: "install kubeadm", run: func(e *Engine) error { return e.installKubeadm() }},
	{Num: 7, Name: "install helm", run: func(e *Engine) error { return e.installHelm() }},
	{Num: 8, Name: "install haproxy", run: func(e *Engine) error { return e.installHa() }},
	{Num: 9, Name: "install keepalived", run: func(e *Engine) error { return e.installKeepalived() }},
	{Num: 10, Name: "start k8s", run: func(e *Engine) error { return e.startK8s() }},
	{Num: 11, Name: "install calico", run: func(e *Engine) error { return e.installCalico() }},
	{Num: 12, Name: "mount storage", run: func(e *Engine) error { return e.installNFS() }},
	{Num: 13, Name: "install istio", run: func(e *Engine) error { return e.installIstio() }},
	{Num: 14, Name: "install app", run: func(e *Engine) error { return e.installApp() }},
}

// update
var UpdateSteps = []*Step{
	{Num: 1, Name: "connect", run: func(e *Engine) error { return e.connect() }},
	{Num: 2, Name: "check new", run: func(e *Engine) error { return e.checkNew() }},
	{Num: 3, Name: "init", run: func(e *Engine) error { return e.init() }},
	{Num: 4, Name: "install chrony", run: func(e *Engine) error { return e.installChrony() }},
	{Num: 5, Name: "install docker", run: func(e *Engine) error { return e.installDocker() }},
	{Num: 6, Name: "load docker image", run: func(e *Engine) error { return e.loadDockerImage() }},
	{Num: 7, Name: "install kubeadm", run: func(e *Engine) error { return e.installKubeadm() }},
	{Num: 8, Name: "install nfs", run: func(e *Engine) error { return e.installNFSUtils() }},
	{Num: 9, Name: "join node", run: func(e *Engine) error { return e.join() }},
}

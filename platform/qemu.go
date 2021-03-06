/*
   Copyright 2015 CoreOS, Inc.

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

package platform

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"

	"code.google.com/p/go-uuid/uuid"

	"github.com/coreos/mantle/platform/local"
	"github.com/coreos/mantle/util"
)

var qemuImage = flag.String("qemu.image", "", "Base disk image")

type qemuCluster struct {
	*local.LocalCluster
	machines []*qemuMachine
}

type qemuMachine struct {
	qc    *qemuCluster
	id    string
	qemu  *exec.Cmd
	netif *local.Interface
}

func NewQemuCluster() (Cluster, error) {
	lc, err := local.NewLocalCluster()
	if err != nil {
		return nil, err
	}

	qc := &qemuCluster{LocalCluster: lc}
	return Cluster(qc), nil
}

func (qc *qemuCluster) Machines() []Machine {
	machines := make([]Machine, len(qc.machines))
	for i, m := range qc.machines {
		machines[i] = m
	}
	return machines
}

func (qc *qemuCluster) Destroy() error {
	for _, qm := range qc.machines {
		qm.Destroy()
	}
	return qc.LocalCluster.Destroy()
}

func (qc *qemuCluster) NewMachine() (Machine, error) {
	qm := &qemuMachine{
		qc:    qc,
		id:    uuid.New(),
		netif: qc.Dnsmasq.GetInterface("br0"),
	}

	disk, err := setupDisk()
	if err != nil {
		return nil, err
	}
	defer disk.Close()

	tap, err := qc.NewTap("br0")
	if err != nil {
		return nil, err
	}
	defer tap.Close()

	qmMac := qm.netif.HardwareAddr.String()
	qmCfg := qm.qc.ConfigDrive.Directory
	qm.qemu = exec.Command(
		"qemu-system-x86_64",
		"-machine", "accel=kvm",
		"-cpu", "host",
		"-smp", "2",
		"-m", "1024",
		"-uuid", qm.id,
		"-display", "none",
		"-add-fd", "fd=3,set=1",
		"-drive", "file=/dev/fdset/1,media=disk,if=virtio",
		"-netdev", "tap,id=tap,fd=4",
		"-device", "virtio-net,netdev=tap,mac="+qmMac,
		"-fsdev", "local,id=cfg,security_model=none,readonly,path="+qmCfg,
		"-device", "virtio-9p-pci,fsdev=cfg,mount_tag=config-2")

	qm.qemu.Stderr = os.Stderr
	qm.qemu.ExtraFiles = append(qm.qemu.ExtraFiles, disk)     // fd=3
	qm.qemu.ExtraFiles = append(qm.qemu.ExtraFiles, tap.File) // fd=4

	if err = qm.qc.CommandStart(qm.qemu); err != nil {
		return nil, err
	}

	out, err := qm.SSH("grep ^ID= /etc/os-release")
	if err != nil {
		qm.Destroy()
		return nil, err
	}

	if !bytes.Equal(out, []byte("ID=coreos")) {
		qm.Destroy()
		return nil, fmt.Errorf("Unexpected SSH output: %s", out)
	}

	qc.machines = append(qc.machines, qm)

	return Machine(qm), nil
}

// Copy the base image to a new temporary file.
func setupDisk() (*os.File, error) {
	srcFile, err := os.Open(*qemuImage)
	if err != nil {
		return nil, err
	}
	defer srcFile.Close()

	dstFile, err := util.TempFile("")
	if err != nil {
		return nil, err
	}

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		dstFile.Close()
		return nil, err
	}

	return dstFile, nil
}

func (m *qemuMachine) ID() string {
	return m.id
}

func (m *qemuMachine) IP() string {
	return m.netif.DHCPv4[0].IP.String()
}

func (qm *qemuMachine) SSH(cmd string) ([]byte, error) {
	client, err := qm.qc.SSHAgent.NewClient(qm.IP())
	if err != nil {
		return []byte{}, err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return []byte{}, err
	}
	defer session.Close()

	session.Stderr = os.Stderr
	out, err := session.Output(cmd)
	out = bytes.TrimSpace(out)
	return out, err
}

func (qm *qemuMachine) Destroy() error {
	qm.qemu.Process.Kill()
	qm.qemu.Wait()
	return nil
}

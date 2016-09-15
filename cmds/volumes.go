/**
 * Copyright (C) 2015 Red Hat, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *         http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */
package cmds

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/fabric8io/gofabric8/client"
	"github.com/fabric8io/gofabric8/util"
	"github.com/spf13/cobra"
	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/resource"
	k8sclient "k8s.io/kubernetes/pkg/client/unversioned"
	cmdutil "k8s.io/kubernetes/pkg/kubectl/cmd/util"
)

const (
	sshCommandFlag = "ssh-command"
)

func NewCmdVolumes(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "volumes",
		Short: "Creates a persisent volume for any pending persistance volume claims",
		Long:  `Creates a persisent volume for any pending persistance volume claims`,
		PreRun: func(cmd *cobra.Command, args []string) {
			showBanner()
		},
		Run: func(cmd *cobra.Command, args []string) {
			c, _ := client.NewClient(f)
			ns, _, err := f.DefaultNamespace()
			if err != nil {
				util.Fatal("No default namespace")
			} else {

				found, pendingClaimNames := findPendingPVS(c, ns)
				if found {
					createPV(c, ns, pendingClaimNames, cmd)
				}
			}
		},
	}
	cmd.PersistentFlags().String(sshCommandFlag, "", "the ssh command to run commands inside the VM of the single node cluster")
	return cmd
}

func findPendingPVS(c *k8sclient.Client, ns string) (bool, []string) {

	pvcs, err := c.PersistentVolumeClaims(ns).List(api.ListOptions{})
	if err != nil {
		util.Infof("Failed to find any PersistentVolumeClaims, %s in namespace %s\n", err, ns)
	}

	if pvcs != nil {
		items := pvcs.Items
		pendingClaimNames := make([]string, 0, len(items))
		for _, item := range items {
			status := item.Status.Phase
			if status == "Pending" || status == "Lost" {
				pendingClaimNames = append(pendingClaimNames, item.ObjectMeta.Name)
			}
		}
		if len(pendingClaimNames) > 0 {
			return true, pendingClaimNames
		}
	}
	return false, nil
}

func createPV(c *k8sclient.Client, ns string, pvcNames []string, cmd *cobra.Command) (Result, error) {

	for _, pvcName := range pvcNames {
		hostPath := "/data/" + pvcName
		pvs := c.PersistentVolumes()
		rc, err := pvs.List(api.ListOptions{})
		if err != nil {
			util.Errorf("Failed to load PersistentVolumes with error %v\n", err)
		}
		items := rc.Items
		for _, volume := range items {
			vname := volume.ObjectMeta.Name
			if vname == pvcName {
				util.Infof("Already created PersistentVolumes for %s\n", pvcName)
			}
		}

		err = configureHostPathVolume(c, ns, hostPath, cmd)
		if err != nil {
			util.Errorf("Failed to configure the host path %s with error %v\n", hostPath, err)
		}

		// lets create a new PV
		util.Infof("PersistentVolume name %s will be created on host path %s\n", pvcName, hostPath)
		pv := api.PersistentVolume{
			ObjectMeta: api.ObjectMeta{
				Name: pvcName,
			},
			Spec: api.PersistentVolumeSpec{
				Capacity: api.ResourceList{
					api.ResourceName(api.ResourceStorage): resource.MustParse("5Gi"),
				},
				AccessModes: []api.PersistentVolumeAccessMode{api.ReadWriteOnce},
				PersistentVolumeSource: api.PersistentVolumeSource{
					HostPath: &api.HostPathVolumeSource{Path: hostPath},
				},
			},
		}

		_, err = pvs.Create(&pv)
		if err != nil {
			util.Errorf("Failed to create PersistentVolume %s at %s with error %v\n", pvcName, hostPath, err)
		}
	}

	return Success, nil
}

// if we are on minikube or minishift lets try to create the
// hostPath folders with relaxed persmissions
func configureHostPathVolume(c *k8sclient.Client, ns string, hostPath string, corbaCmd *cobra.Command) error {
	cli := ""
	flag := corbaCmd.Flags().Lookup(sshCommandFlag)
	if flag != nil {
		cli = flag.Value.String()
	}

	if len(cli) == 0 {
		context, isMini, _ := util.GetMiniType()
		if isMini {
			cli = context
		}
	}
	if len(cli) == 0 {
		// lets default to using vagrant if we have a Vagrantfile
		if _, err := os.Stat("Vagrantfile"); err == nil {
			cli = "vagrant"
		}
	}
	if len(cli) == 0 {
		return nil
	}

	shellCommands := []string{
		fmt.Sprintf("sudo mkdir -p %s", hostPath),
		fmt.Sprintf("sudo chmod 777 %s", hostPath),
		fmt.Sprintf("echo hostPath is setup correctly at: %s", hostPath),
	}
	util.Infof("About to modify host paths on the VM via the command: %s\n", cli)

	for _, shellCmd := range shellCommands {
		args := []string{"ssh", fmt.Sprintf("/bin/sh -c '%s'", shellCmd)}
		cmd := exec.Command(cli, args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		var waitStatus syscall.WaitStatus
		if err := cmd.Run(); err != nil {
			printErr(err)
			if exitError, ok := err.(*exec.ExitError); ok {
				waitStatus = exitError.Sys().(syscall.WaitStatus)
				printStatus(waitStatus.ExitStatus())
			}
			return err
		} else {
			waitStatus = cmd.ProcessState.Sys().(syscall.WaitStatus)
			printStatus(waitStatus.ExitStatus())
		}
	}
	return nil
}

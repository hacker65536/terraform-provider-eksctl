package cluster

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"github.com/mumoshu/terraform-provider-eksctl/pkg/resource"
)

func (m *Manager) createCluster(d *schema.ResourceData) (*ClusterSet, error) {
	id := newClusterID()

	log.Printf("[DEBUG] creating eksctl cluster with id %q", id)

	set, err := m.PrepareClusterSet(d, id)
	if err != nil {
		return nil, err
	}

	cluster := set.Cluster

	if err := createVPCResourceTags(cluster, set.ClusterName); err != nil {
		return nil, err
	}

	cmd, err := newEksctlCommandWithAWSProfile(cluster, "create", "cluster", "-f", "-")
	if err != nil {
		return nil, fmt.Errorf("creating eksctl-create command: %w", err)
	}

	cmd.Stdin = bytes.NewReader(set.ClusterConfig)

	if err := resource.Create(cmd, d, id); err != nil {
		return nil, fmt.Errorf("running `eksctl create cluster: %w: USED CLUSTER CONFIG:\n%s", err, string(set.ClusterConfig))
	}

	if err := ensureIAMIdentityMapping(d, cluster); err != nil {
		return nil, fmt.Errorf("Can not get iamidentity: %w", err)
	}

	if err := doWriteKubeconfig(d, string(set.ClusterName), cluster.Region); err != nil {
		return nil, err
	}

	if err := doApplyKubernetesManifests(cluster, id); err != nil {
		return nil, err
	}

	if err := doAttachAutoScalingGroupsToTargetGroups(set); err != nil {
		return nil, err
	}

	if err := doCheckPodsReadiness(cluster, id); err != nil {
		return nil, err
	}

	return set, nil
}

func (m *Manager) doPlanKubeconfig(d *DiffReadWrite) error {
	var path string

	if v := d.Get(KeyKubeconfigPath); v != nil {
		path = v.(string)
	}

	if path == "" {
		d.SetNewComputed(KeyKubeconfigPath)
	}

	return nil
}

func doWriteKubeconfig(d ReadWrite, clusterName, region string) error {
	var path string

	if v := d.Get(KeyKubeconfigPath); v != nil {
		path = v.(string)
	}

	if path == "" {
		kubeconfig, err := ioutil.TempFile(os.TempDir(), "tf-eksctl-kubeconfig")
		if err != nil {
			return fmt.Errorf("failed generating kubeconfig path: %w", err)
		}
		_ = kubeconfig.Close()

		path = kubeconfig.Name()

		d.Set(KeyKubeconfigPath, path)
	}

	cmd, err := newEksctlCommandFromResourceWithRegionAndProfile(d, "utils", "write-kubeconfig", "--cluster", clusterName)
	if err != nil {
		return fmt.Errorf("creating eksctl-utils-write-kubeconfig command: %w", err)
	}

	cmd.Env = append(cmd.Env, os.Environ()...)
	cmd.Env = append(cmd.Env, "KUBECONFIG="+path)

	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed running %s %s: %vw: COMBINED OUTPUT:\n%s", cmd.Path, strings.Join(cmd.Args, " "), err, string(out))
	}

	log.Printf("Ran `%s %s` with KUBECONFIG=%s", cmd.Path, strings.Join(cmd.Args, " "), path)

	kubectlBin := "kubectl"
	if v := d.Get(KeyKubectlBin); v != nil {
		s := v.(string)
		if s != "" {
			kubectlBin = s
		}
	}

	retries := 5
	retryDelay := 5 * time.Second
	for i := 0; i < retries; i++ {
		kubectlVersion := exec.Command(kubectlBin, "version")
		kubectlVersion.Env = append(cmd.Env, os.Environ()...)
		kubectlVersion.Env = append(cmd.Env, "KUBECONFIG="+path)

		out, err := kubectlVersion.CombinedOutput()
		if err == nil {
			break
		}

		log.Printf("Retrying kubectl version error with KUBECONFIG=%s: %v: COMBINED OUTPUT:\n%s", path, err, string(out))
		time.Sleep(retryDelay)
	}

	return nil
}

func ensureIAMIdentityMapping(d *schema.ResourceData, cluster *Cluster) error {

	// get current iamidentitymapping

	roles := d.Get(KeyIAMIdentityMapping).([]interface{})
	log.Printf("iamidentitymapping: %v", roles)
	/*
		for _, v := range roles {
			opt := []string{
				"create",
				"iamidentitymapping",
				"--cluster",
				cluster.Name,
				"--arn",
				v.(string),
				"--group",
				"system:masters",
				"--username",
				"freee-sso-admin",
			}
			cmd, err := newEksctlCommandWithAWSProfile(cluster, opt...)

			if err != nil {
				return fmt.Errorf("creating imaidentitymapping command: %w", err)
			}
			if _, err := resource.Run(cmd); err != nil {
				return fmt.Errorf("running `eksctl create iamidentitymapping : %w", err)
			}
		}
	*/
	return nil
}

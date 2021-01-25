/*
Copyright 2020

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

package metricsprovider

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"github.com/paypal/load-watcher/pkg/watcher"
	log "github.com/sirupsen/logrus"
	"net/http"
	"os"
	"reflect"
	"strconv"

	// For out of cluster connections.
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
)

var (
	promHost    string
	promToken	string
	promTokenPresent = false
	node_metric_query = map[string]string{
		watcher.CPU : 	"instance:node_cpu:ratio",
		watcher.Memory : "instance:node_memory_utilization:ratio",
	}

)

const (
	// env variable that provides path to kube config file, if deploying from outside K8s cluster
	promHostKey = "PROM_HOST"
	promTokenKey = "PROM_TOKEN"
	promQuery = "/api/v1/query?query="
	prom_std_method = "stddev_over_time"
	prom_avg_method = "avg_over_time"
	prom_cpu_metric = "instance:node_cpu:ratio"
	prom_mem_metric = "instance:node_memory_utilisation:ratio"
)

func init() {
	var promHostPresent bool

	promHost, promHostPresent = os.LookupEnv(promHostKey)
	promToken, promTokenPresent = os.LookupEnv(promTokenKey)
	if !promHostPresent {
		promHost = "prometheus-k8s:9090"
	}
}

type promClient struct {
	client http.Client
}

func NewPromClient() (watcher.FetcherClient, error) {
	tlsConfig := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	return promClient{client: http.Client{
		Timeout:   httpClientTimeout,
		Transport: tlsConfig}}, nil
}

// Fetch all host metrics for all methods and resource types
func (s promClient) FetchAllHostsMetrics(window *watcher.Window) (map[string][]watcher.Metric, error) {
	hostMetrics := make(map[string][]watcher.Metric)

	for _, method := range []string{prom_avg_method, prom_std_method} {
		for _, metric := range []string{prom_cpu_metric, prom_mem_metric} {
			hostMetrics = s.updateAllHostMetrics(hostMetrics, metric, method, window.Duration)
		}
	}

	return hostMetrics, nil
}

// Fetch all host metrics for a particular method and resource type.
func (s promClient) updateAllHostMetrics(hostMetrics map[string][]watcher.Metric, metric string, method string, rollup string) map[string][]watcher.Metric {
	promURLStr := fmt.Sprintf("http://%s%s%s(%s[%s])", promHost,
		promQuery, method, metric, rollup)
	req, _ := http.NewRequest(http.MethodGet, promURLStr, nil)
	req.Header.Set("Content-Type", "application/json")

	if promTokenPresent {
		tokenStr := fmt.Sprintf("Bearer %s", promToken)
		req.Header.Set("Authorization", tokenStr)
	}

	resp, _ := s.client.Do(req)

	if resp.StatusCode != http.StatusOK {
		log.Printf("received status code: %v", resp.StatusCode)
		return hostMetrics
	}

	var res map[string]map[string]interface{}
	err := json.NewDecoder(resp.Body).Decode(&res)
	if err != nil {
		log.Printf("error parsing the response: %v", err)
	}

	if promdata, ok := res["data"]["result"]; ok {
		log.Printf("receive response type: %v", reflect.TypeOf(promdata))

		switch promdata.(type) {
		case []interface{}:
			fmt.Printf("response data is a slice of interface: %v \n ", promdata)
			for _, prom_metric := range promdata.([]interface{}) { // use type assertion to loop over []interface{}
				log.Printf("metric object: %v", prom_metric)
				curMetric, curHost := promdata2metric(prom_metric.(map[string]interface{}), metric, method, rollup)
				hostMetrics[curHost] = append(hostMetrics[curHost], curMetric)
			}
		case map[string]interface{}:
			fmt.Printf("%v is a slice of interface \n ", promdata)
			curMetric, curHost := promdata2metric(promdata.(map[string]interface{}), metric, method, rollup)
			hostMetrics[curHost] = append(hostMetrics[curHost], curMetric)
		default:
			log.Printf("%v is not recognized prometheus data format \n", promdata)
		}
	} else {
		log.Printf("not able to parse prometheus query response: %v", res)
	}

	return hostMetrics
}

// Convert Json object from Prometheus query to watcher.Metric object.
func promdata2metric(promdata map[string]interface{}, metric string, method string, rollup string) (watcher.Metric, string) {
	var curMetric watcher.Metric
	var curHost string
	curMetric.Name = method
	curMetric.Rollup = rollup

	// TODO: define a consistent metric name and metric type across all types of clients.

	if metric == prom_cpu_metric {
		curMetric.Type = watcher.CPU
	} else {
		curMetric.Type = watcher.Memory
	}

	for k, v := range promdata { // use type assertion to loop over []interface{}
		log.Printf("metric key: %v", k)
		log.Printf("metric value: %v", v)

		if k == "metric" {
			if _, ok := v.(map[string]interface {}); ok {
				curHost = v.(map[string]interface {})["instance"].(string)
			}
		} else {
			curMetric.Value, _ = strconv.ParseFloat(v.([]interface{})[1].(string), 64)
		}
	}

	return curMetric, curHost
}
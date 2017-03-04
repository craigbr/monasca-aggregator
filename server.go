// Copyright 2017 Hewlett Packard Enterprise Development LP
//
//    Licensed under the Apache License, Version 2.0 (the "License"); you may
//    not use this file except in compliance with the License. You may obtain
//    a copy of the License at
//
//         http://www.apache.org/licenses/LICENSE-2.0
//
//    Unless required by applicable law or agreed to in writing, software
//    distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
//    WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
//    License for the specific language governing permissions and limitations
//    under the License.

package main

import (
	log "github.com/Sirupsen/logrus"
	"os"
	"github.com/confluentinc/confluent-kafka-go/kafka"
	"fmt"
	"os/signal"
	"syscall"
	"github.hpe.com/UNCLE/monasca-aggregation/models"
	"encoding/json"
	"time"
)

const aggregationPeriod = time.Minute

const aggregationSpecifications = []models.AggregationSpecification{
	{"Aggregation1", "metric1", "aggregated-metric1"},
	{"Aggregation2", "metric2", "aggregated-metric2"},
}

var aggregations = map[string]float64{}

func initLogging() {
	// Log as JSON instead of the default ASCII formatter.
	log.SetFormatter(&log.JSONFormatter{})
	log.SetOutput(os.Stdout)
	log.SetLevel(log.InfoLevel)
}

// TODO: Read in kafka configuration parameters from yaml file
// TODO: Read in aggregation period and aggregation specifications from yaml file
// TODO: Publish aggregated metrics to Kafka
// TODO: Manually update Kafka offsets such that if a crash occurs, processing re-starts at the correct offset
// TODO: Create aggreagations to handle both late arriving and early arriving metrics based on the metric timestamp and allowed tolerance
// TODO: Handle metrics that arrive late or in the future within some tolerance of the current periods start/end time
// TODO: Use the metric timestamp (event time) to accumulate the aggregation into the appropriate last, current and future aggregation window
// TODO: Add support for grouping on dimensions
// TODO: Publish aggregations at discrete intervals + lag time. For example, 10 minutes past the hour.
// TODO: Add Prometheus Client library and report metrics
// TODO: Create Helm Charts
// TODO: Add support for consuming/publishing intermediary aggregations. For example, publish a (sum, count) to use in an avg aggregation
func main() {
	initLogging()

	if len(os.Args) < 4 {
		fmt.Fprintf(os.Stderr, "Usage: %s <broker> <group> <topics..>\n",
			os.Args[0])
		os.Exit(1)
	}

	broker := os.Args[1]
	group := os.Args[2]
	topics := os.Args[3:]

	sigchan := make(chan os.Signal)
	signal.Notify(sigchan, syscall.SIGINT, syscall.SIGTERM)

	c, err := kafka.NewConsumer(&kafka.ConfigMap{
		"bootstrap.servers":               broker,
		"group.id":                        group,
		"session.timeout.ms":              6000,
		"go.events.channel.enable":        true,
		"go.application.rebalance.enable": true,
		"default.topic.config":            kafka.ConfigMap{"auto.offset.reset": "earliest"}})

	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create consumer: %s\n", err)
		os.Exit(1)
	}

	fmt.Printf("Created Consumer %v\n", c)

	err = c.SubscribeTopics(topics, nil)

	aggregationTicker := time.NewTicker(aggregationPeriod)

	run := true

	for run == true {
		select {
		case sig := <-sigchan:
			fmt.Printf("Caught signal %v: terminating\n", sig)
			run = false

		case ev := <-c.Events():
			switch e := ev.(type) {
			case kafka.AssignedPartitions:
				fmt.Fprintf(os.Stderr, "%% %v\n", e)
				c.Assign(e.Partitions)
			case kafka.RevokedPartitions:
				fmt.Fprintf(os.Stderr, "%% %v\n", e)
				c.Unassign()
			case *kafka.Message:
				metricEnvelope := models.MetricEnvelope{}
				err = json.Unmarshal([]byte(e.Value), &metricEnvelope)
				if err != nil {
					log.Warn("%% Invalid metric envelope on %s:\n%s\n",
						e.TopicPartition, string(e.Value))
				} else {
					var metric = metricEnvelope.Metric
					for _, aggregationSpecification := range aggregationSpecifications {
						if metric.Name == aggregationSpecification.OriginalMetricName {
							aggregations[aggregationSpecification.AggregatedMetricName] += metric.Value
						}
					}
					fmt.Print(metricEnvelope)
				}
			case kafka.PartitionEOF:
				fmt.Printf("%% Reached %v\n", e)
			case kafka.Error:
				fmt.Fprintf(os.Stderr, "%% Error: %v\n", e)
				run = false
			}

		case <-aggregationTicker.C:
			fmt.Print(aggregations)
			aggregations = map[string]float64{}
		}
	}

	fmt.Printf("Closing consumer\n")
	c.Close()
}

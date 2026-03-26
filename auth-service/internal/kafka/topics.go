package kafka

import (
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

// EnsureTopics creates Kafka topics if they don't already exist.
// This prevents the consumer group partition assignment race condition
// where consumers join before topics are created by producers.
func EnsureTopics(broker string, topics ...string) {
	addr := strings.Split(broker, ",")[0]

	var conn *kafkago.Conn
	var err error

	for i := 0; i < 10; i++ {
		conn, err = kafkago.Dial("tcp", addr)
		if err == nil {
			break
		}
		log.Printf("waiting for kafka (%d/10): %v", i+1, err)
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		log.Printf("warn: could not connect to kafka to ensure topics: %v", err)
		return
	}
	defer conn.Close()

	controller, err := conn.Controller()
	if err != nil {
		log.Printf("warn: could not get kafka controller: %v", err)
		return
	}

	controllerConn, err := kafkago.Dial("tcp", net.JoinHostPort(controller.Host, strconv.Itoa(controller.Port)))
	if err != nil {
		log.Printf("warn: could not connect to kafka controller: %v", err)
		return
	}
	defer controllerConn.Close()

	topicConfigs := make([]kafkago.TopicConfig, len(topics))
	for i, t := range topics {
		topicConfigs[i] = kafkago.TopicConfig{
			Topic:             t,
			NumPartitions:     1,
			ReplicationFactor: 1,
		}
	}

	if err := controllerConn.CreateTopics(topicConfigs...); err != nil {
		log.Printf("warn: topic creation error (topics may already exist): %v", err)
	} else {
		log.Printf("kafka topics ensured: %v", topics)
	}
}

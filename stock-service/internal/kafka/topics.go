package kafka

import (
	"log"
	"net"
	"strconv"
	"time"

	kafkalib "github.com/segmentio/kafka-go"
)

func EnsureTopics(broker string, topics ...string) {
	var conn *kafkalib.Conn
	var err error
	for i := 0; i < 10; i++ {
		conn, err = kafkalib.Dial("tcp", broker)
		if err == nil {
			break
		}
		log.Printf("kafka dial attempt %d: %v", i+1, err)
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		log.Printf("WARN: could not connect to kafka to create topics: %v", err)
		return
	}
	defer conn.Close()

	controller, err := conn.Controller()
	if err != nil {
		log.Printf("WARN: could not get kafka controller: %v", err)
		return
	}
	addr := net.JoinHostPort(controller.Host, strconv.Itoa(controller.Port))
	controllerConn, err := kafkalib.Dial("tcp", addr)
	if err != nil {
		log.Printf("WARN: could not connect to kafka controller: %v", err)
		return
	}
	defer controllerConn.Close()

	topicConfigs := make([]kafkalib.TopicConfig, len(topics))
	for i, t := range topics {
		topicConfigs[i] = kafkalib.TopicConfig{
			Topic:             t,
			NumPartitions:     1,
			ReplicationFactor: 1,
		}
	}
	if err := controllerConn.CreateTopics(topicConfigs...); err != nil {
		log.Printf("WARN: create topics: %v", err)
	}
}

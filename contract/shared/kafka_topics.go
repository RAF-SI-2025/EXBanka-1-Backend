// Package shared — kafka_topics.go is the canonical EnsureTopics helper.
// Replaces the byte-identical internal/kafka/topics.go file shipped in
// every service.
//
// CLAUDE.md mandates pre-creation of every topic a service produces or
// consumes, because Kafka consumer groups that join before topics exist
// get assigned 0 partitions and silently never receive messages. The
// retry loop here handles startup ordering in Docker Compose where Kafka
// may take a few seconds to become reachable after the service starts.
package shared

import (
	"net"
	"strconv"
	"strings"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

// EnsureTopicsConfig controls retry behavior. Sensible defaults are used
// when the zero value is passed (10 attempts, 2s apart, 1 partition,
// replication factor 1).
type EnsureTopicsConfig struct {
	// MaxAttempts to dial the Kafka broker before giving up. Each failed
	// attempt waits AttemptDelay before retrying. After the budget is
	// exhausted, EnsureTopics returns without error so the caller's
	// boot sequence is not blocked — the service still starts and
	// dependent consumers will receive messages once topics eventually
	// appear.
	MaxAttempts int

	// AttemptDelay between dial attempts.
	AttemptDelay time.Duration

	// NumPartitions for newly created topics. Defaults to 1 — sufficient
	// for the in-monorepo throughput. Override if you wire a topic
	// expected to fan out across multiple partitions.
	NumPartitions int

	// ReplicationFactor for newly created topics. Defaults to 1 — the
	// dev/CI Kafka has a single broker. In production with a multi-node
	// cluster, set this to 3 or higher.
	ReplicationFactor int
}

// DefaultEnsureTopicsConfig is the configuration used when EnsureTopics is
// called without an explicit config.
var DefaultEnsureTopicsConfig = EnsureTopicsConfig{
	MaxAttempts:       10,
	AttemptDelay:      2 * time.Second,
	NumPartitions:     1,
	ReplicationFactor: 1,
}

// EnsureTopics idempotently creates the named topics on the broker. Safe
// to call repeatedly — existing topics are left as-is and the call returns
// without error.
//
// Failures are logged but never panic. A service must boot even if its
// topics cannot be pre-created right now: the application code still works
// once the broker becomes available, and a future restart will succeed.
func EnsureTopics(broker string, topics ...string) {
	EnsureTopicsWithConfig(broker, DefaultEnsureTopicsConfig, topics...)
}

// EnsureTopicsWithConfig is EnsureTopics with explicit retry / partition
// settings. Pass DefaultEnsureTopicsConfig for the zero-config path.
func EnsureTopicsWithConfig(broker string, cfg EnsureTopicsConfig, topics ...string) {
	if len(topics) == 0 {
		return
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = DefaultEnsureTopicsConfig.MaxAttempts
	}
	if cfg.AttemptDelay <= 0 {
		cfg.AttemptDelay = DefaultEnsureTopicsConfig.AttemptDelay
	}
	if cfg.NumPartitions <= 0 {
		cfg.NumPartitions = DefaultEnsureTopicsConfig.NumPartitions
	}
	if cfg.ReplicationFactor <= 0 {
		cfg.ReplicationFactor = DefaultEnsureTopicsConfig.ReplicationFactor
	}

	addr := strings.Split(broker, ",")[0]

	var conn *kafkago.Conn
	var err error
	for i := 0; i < cfg.MaxAttempts; i++ {
		conn, err = kafkago.Dial("tcp", addr)
		if err == nil {
			break
		}
		defaultLog("waiting for kafka (%d/%d): %v", i+1, cfg.MaxAttempts, err)
		time.Sleep(cfg.AttemptDelay)
	}
	if err != nil {
		defaultLog("warn: could not connect to kafka to ensure topics: %v", err)
		return
	}
	defer conn.Close()

	controller, err := conn.Controller()
	if err != nil {
		defaultLog("warn: could not get kafka controller: %v", err)
		return
	}

	controllerConn, err := kafkago.Dial("tcp", net.JoinHostPort(controller.Host, strconv.Itoa(controller.Port)))
	if err != nil {
		defaultLog("warn: could not connect to kafka controller: %v", err)
		return
	}
	defer controllerConn.Close()

	configs := make([]kafkago.TopicConfig, len(topics))
	for i, t := range topics {
		configs[i] = kafkago.TopicConfig{
			Topic:             t,
			NumPartitions:     cfg.NumPartitions,
			ReplicationFactor: cfg.ReplicationFactor,
		}
	}
	if err := controllerConn.CreateTopics(configs...); err != nil {
		// CreateTopics returns an error if any topic already exists. That's
		// the expected steady-state — log at INFO/warn level and move on.
		defaultLog("warn: topic creation (some topics may already exist): %v", err)
		return
	}
	defaultLog("kafka topics ensured: %v", topics)
}

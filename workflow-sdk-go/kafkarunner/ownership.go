package kafkarunner

import (
	"context"
	"sync"

	"github.com/twmb/franz-go/pkg/kgo"
)

type partitionKey struct {
	topic     string
	partition int32
}
type assignment struct {
	ctx    context.Context
	cancel context.CancelFunc
}

// An assignment has its own lifetime, including when the same partition is
// revoked and then assigned back to this client. Old work must not commit in
// the new assignment. Kafka heartbeats/rebalances continue during handler work.
type ownership struct {
	mu         sync.Mutex
	parent     context.Context
	partitions map[partitionKey]*assignment
}

func newOwnership(ctx context.Context) *ownership {
	return &ownership{parent: ctx, partitions: map[partitionKey]*assignment{}}
}

func (o *ownership) assigned(_ context.Context, _ *kgo.Client, topics map[string][]int32) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for topic, partitions := range topics {
		for _, partition := range partitions {
			key := partitionKey{topic, partition}
			if o.partitions[key] != nil {
				continue
			}
			ctx, cancel := context.WithCancel(o.parent)
			o.partitions[key] = &assignment{ctx, cancel}
		}
	}
}

func (o *ownership) revoked(_ context.Context, _ *kgo.Client, topics map[string][]int32) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for topic, partitions := range topics {
		for _, partition := range partitions {
			key := partitionKey{topic, partition}
			if old := o.partitions[key]; old != nil {
				old.cancel()
				delete(o.partitions, key)
			}
		}
	}
}

func (o *ownership) get(rec *kgo.Record) *assignment {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.partitions[partitionKey{rec.Topic, rec.Partition}]
}

func (o *ownership) commit(rec *kgo.Record, owner *assignment, commit func(context.Context) error) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.partitions[partitionKey{rec.Topic, rec.Partition}] != owner || owner.ctx.Err() != nil {
		return context.Canceled
	}
	// The caller bounds this network operation. Revocation waits for this
	// synchronous commit before relinquishing ownership; never commit after it.
	return commit(owner.ctx)
}

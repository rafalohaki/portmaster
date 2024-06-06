package rng

import (
	"context"
	"encoding/binary"
	"time"
)

func getTickFeederTickDuration() time.Duration {
	// be ready in 1/10 time of reseedAfterSeconds
	msecsAvailable := reseedAfterSeconds * 100
	// ex.: reseed after 10 minutes: msecsAvailable = 60000
	// have full entropy after 5 minutes

	// one tick generates 0,125 bits of entropy
	ticksNeeded := minFeedEntropy * 8
	// ex.: minimum entropy is 256: ticksNeeded = 2048

	// msces between ticks
	tickMsecs := msecsAvailable / ticksNeeded
	// ex.: tickMsecs = 29(,296875)

	// use a minimum of 10 msecs per tick for good entropy
	// it would take 21 seconds to get full 256 bits of entropy with 10msec ticks
	if tickMsecs < 10 {
		tickMsecs = 10
	}

	return time.Duration(tickMsecs) * time.Millisecond
}

// tickFeeder is a really simple entropy feeder that adds the least significant bit of the current nanosecond unixtime to its pool every time it 'ticks'.
// The more work the program does, the better the quality, as the internal schedular cannot immediately run the goroutine when it's ready.
func tickFeeder(ctx context.Context) error {
	var value int64
	var pushes int
	feeder := NewFeeder()
	defer feeder.CloseFeeder()

	tickDuration := getTickFeederTickDuration()

	for {
		// wait for tick
		time.Sleep(tickDuration)

		// add tick value
		value = (value << 1) | (time.Now().UnixNano() % 2)
		pushes++

		if pushes >= 64 {
			// convert to []byte
			b := make([]byte, 8)
			binary.LittleEndian.PutUint64(b, uint64(value))
			// reset
			pushes = 0

			// feed
			select {
			case feeder.input <- &entropyData{
				data:    b,
				entropy: 8,
			}:
			case <-ctx.Done():
				return nil
			}
		} else {
			// check if are done
			select {
			case <-ctx.Done():
				return nil
			default:
			}
		}
	}
}

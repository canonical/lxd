package benchmark

import (
	"runtime"
	"sync"
	"time"
)

func getBatchSize(parallel int) int {
	if parallel > 0 {
		return parallel
	}

	return runtime.NumCPU()
}

func processBatch(count int, batchSize int, process func(index int, wg *sync.WaitGroup)) time.Duration {
	batches := count / batchSize
	remainder := count % batchSize
	processed := 0
	wg := sync.WaitGroup{}
	nextStat := batchSize

	logf("Batch processing start")
	timeStart := time.Now()

	for range batches {
		for range batchSize {
			wg.Add(1)
			go process(processed, &wg)
			processed++
		}

		wg.Wait()

		if processed >= nextStat {
			interval := time.Since(timeStart).Seconds()
			logf("Processed %d containers in %.3fs (%.3f/s)", processed, interval, float64(processed)/interval)
			nextStat = nextStat * 2
		}
	}

	for range remainder {
		wg.Add(1)
		go process(processed, &wg)
		processed++
	}

	wg.Wait()

	timeEnd := time.Now()
	duration := timeEnd.Sub(timeStart)
	logf("Batch processing completed in %.3fs", duration.Seconds())
	return duration
}

package engine

import (
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/sync/errgroup"
)

func (e *Engine) Install(steps string) error {
	if err := e.check(); err != nil {
		return err
	}
	defer e.closeAll()
	if len(steps) > 0 {
		nums, err := parseStepNums(steps, len(DeploySteps))
		if err != nil {
			return err
		}
		nums = prependMissingSteps(nums, 1)
		for _, n := range nums {
			if err := DeploySteps[n-1].install(e); err != nil {
				return err
			}
		}
		return nil
	}

	for _, step := range DeploySteps {
		if err := step.install(e); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) Update(steps string) error {
	if err := e.check(); err != nil {
		return err
	}
	defer e.closeAll()
	if len(steps) > 0 {
		nums, err := parseStepNums(steps, len(UpdateSteps))
		if err != nil {
			return err
		}
		required := []int{1}
		for _, n := range nums {
			if n > 2 {
				required = append(required, 2)
				break
			}
		}
		nums = prependMissingSteps(nums, required...)
		for _, n := range nums {
			if err := UpdateSteps[n-1].install(e); err != nil {
				return err
			}
		}
		return nil
	}
	for _, step := range UpdateSteps {
		if err := step.install(e); err != nil {
			return err
		}
	}
	return nil
}

func parseStepNums(raw string, max int) ([]int, error) {
	var nums []int
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		n, err := strconv.Atoi(item)
		if err != nil {
			return nil, fmt.Errorf("invalid step %q: %w", item, err)
		}
		if n < 1 || n > max {
			return nil, fmt.Errorf("invalid step %d: valid range is 1-%d", n, max)
		}
		nums = append(nums, n)
	}
	if len(nums) == 0 {
		return nil, fmt.Errorf("no steps selected")
	}
	return nums, nil
}

func prependMissingSteps(nums []int, required ...int) []int {
	added := make(map[int]struct{}, len(nums)+len(required))
	out := make([]int, 0, len(nums)+len(required))
	for _, n := range required {
		if _, ok := added[n]; ok {
			continue
		}
		out = append(out, n)
		added[n] = struct{}{}
	}
	for _, n := range nums {
		if _, ok := added[n]; ok {
			continue
		}
		out = append(out, n)
		added[n] = struct{}{}
	}
	return out
}

func (e *Engine) closeAll() {
	for _, n := range e.nodes {
		n.Close()
	}
}

func (e *Engine) connect() error {
	eg, ctx := errgroup.WithContext(e.ctx)
	for i := range e.nodes {
		n := e.nodes[i]
		eg.Go(func() error {
			return n.ConnectContext(ctx)
		})
	}
	return eg.Wait()
}

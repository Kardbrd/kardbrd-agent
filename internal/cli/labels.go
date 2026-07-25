package cli

import (
	"fmt"
	"sort"
)

type labelReconciliation struct {
	Additions []string
	Removals  []string
}

func reconcileLabelIDs(current, valid, desired []string) (labelReconciliation, error) {
	currentSet := labelIDSet(current)
	validSet := labelIDSet(valid)
	desiredSet := labelIDSet(desired)

	for _, labelID := range sortedLabelIDs(desiredSet) {
		if !validSet[labelID] {
			return labelReconciliation{}, fmt.Errorf("label ID %q is not defined on this card's board", labelID)
		}
	}

	plan := labelReconciliation{}
	for _, labelID := range sortedLabelIDs(desiredSet) {
		if !currentSet[labelID] {
			plan.Additions = append(plan.Additions, labelID)
		}
	}
	for _, labelID := range sortedLabelIDs(currentSet) {
		if !desiredSet[labelID] {
			plan.Removals = append(plan.Removals, labelID)
		}
	}
	return plan, nil
}

func labelIDSet(labelIDs []string) map[string]bool {
	set := make(map[string]bool, len(labelIDs))
	for _, labelID := range labelIDs {
		set[labelID] = true
	}
	return set
}

func sortedLabelIDs(set map[string]bool) []string {
	labelIDs := make([]string, 0, len(set))
	for labelID := range set {
		labelIDs = append(labelIDs, labelID)
	}
	sort.Strings(labelIDs)
	return labelIDs
}

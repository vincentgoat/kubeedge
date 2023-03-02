package accessmixer

import (
	"k8s.io/apimachinery/pkg/api/equality"
)

func containsSubject(new []interface{}, oldItem interface{}) bool {
	for _, newItem := range new {
		if equality.Semantic.DeepEqual(newItem, oldItem) {
			return true
		}
	}
	return false
}

func compareSubjects(old, new interface{}) bool {
	switch n := new.(type) {
	case []interface{}:
		switch o := old.(type) {
		case []interface{}:
			if len(o) != len(n) {
				return false
			}
			for _, oldItem := range o {
				if !containsSubject(n, oldItem) {
					return false
				}
			}
			return true
		}
	}
	return false
}

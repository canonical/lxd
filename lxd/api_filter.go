package main

// verify which imports are needed
import (
	"strings"
	"reflect"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/api"
)

// Parse a filter, and then apply over list of structs
func doFilter (fstr string, result []interface{}) []interface{} {
	filter := []*FilterEntry{}

	filterSplit := strings.Fields(fstr)

	index := 0
	prevLogical := "and"

	queryLen := len(filterSplit)

	for index < queryLen {
		entry := FilterEntry{}
		if strings.EqualFold(filterSplit[index], "not") {
			entry.Not = true
			index++
		} else {
			entry.Not = false
		}

		entry.Field = filterSplit[index]
		entry.Operator = filterSplit[index+1]
		value := filterSplit[index+2]
		index+=3

		// support strings with spaces that are quoted
		if strings.HasPrefix(value, "\"") {
			value = value[1:len(value)]
			for !strings.HasSuffix(filterSplit[index], "\"") {
				value = value + " " + filterSplit[index]
				index++
			}
			end := filterSplit[index]
			value = value + " " + end[0:len(end)-1]
			index++
		}
		entry.Value = value

		entry.PrevLogical = prevLogical
		if index < queryLen {
			prevLogical = filterSplit[index]
			index++
		}
		filter = append(filter, &entry)
	}

	// Apply filter over list
	newResult := result[:0]
	for _,obj := range result {
		if applyFilter(filter, obj) {
			newResult = append(newResult, obj)
		}
	}
	return newResult
}

// Apply a filter to a single object
func applyFilter (filter []*FilterEntry, obj interface{}) bool {
	result := true

	for _,entry := range filter {
		curResult := false
		
		// Pass to eval function of correct type
		objType := reflect.TypeOf(obj).String()
		switch (objType) {
			case "*api.Instance":
				curResult = evaluateFieldInstance(*entry, obj.(*api.Instance))
				break
			case "*api.InstanceFull":
				curResult = evaluateFieldInstanceFull(*entry, obj.(*api.InstanceFull))
				break
			case "*api.Image":
				curResult = evaluateFieldImage(*entry, obj.(*api.Image))
				break
			default:
				logger.Error("Error while filtering: unable to identify type")
				return false
		}


		// Finish out logic
		if entry.Not {
			curResult = !curResult
		}

		if strings.EqualFold (entry.PrevLogical, "and") {
			result = curResult && result
		} else {
			if strings.EqualFold(entry.PrevLogical, "or") {
				result = curResult || result
			}
		}
	}

	return result
}
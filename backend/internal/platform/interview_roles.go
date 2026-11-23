package platform

import "strings"

// Interview jobs are a fixed display list. Programmer collections still follow
// the technical tags. Every other job returns only collections stored in
// interview_collection_roles, never collections inferred from hot or web3 tags.
var interviewJobs = []map[string]string{
	{"slug": "programmer", "name": "程序员"},
	{"slug": "design", "name": "设计"},
	{"slug": "cross-border-ops", "name": "跨境电商运营"},
	{"slug": "crypto-bd", "name": "加密行业BD"},
	{"slug": "cross-border-bd", "name": "跨境电商BD"},
	{"slug": "crypto-ops", "name": "加密行业运营"},
}

func interviewJob(slug string) (map[string]string, bool) {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		slug = "programmer"
	}
	for _, job := range interviewJobs {
		if job["slug"] == slug {
			return job, true
		}
	}
	return nil, false
}

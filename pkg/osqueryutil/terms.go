package osqueryutil

import "github.com/defensestation/osquery"

func TermsFromStrings(field string, values []string) *osquery.TermsQuery {
	terms := make([]interface{}, 0, len(values))
	for _, value := range values {
		terms = append(terms, value)
	}
	return osquery.Terms(field, terms...)
}

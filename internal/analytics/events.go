package analytics

// Schema is stamped on every line. Raise it when an event keeps its name but
// changes meaning, so rows counted under one definition can be told apart from
// rows counted under the next.
const Schema = 1

// IdeaCreated is an idea saved.
type IdeaCreated struct{}

func (IdeaCreated) EventName() string { return "idea_created" }

// All is every event this app emits. Written out by hand: the schema tests fail
// when a type with an EventName method is missing here.
func All() []Event {
	return []Event{
		IdeaCreated{},
	}
}

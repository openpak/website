package pages

// titleNames maps title ids to names, for the titles OpenPak actually serves.
//
// Deliberately two entries. An earlier version of this file listed half a dozen
// popular games copied from a competitor's status page: none of them run
// against this network, and one of the ids was not even a game. A status page
// that lists titles it does not serve is advertising, and a wrong id is a lie
// with a number next to it.
//
// An id with no name here is printed as the id. That is honest, and it is how
// a new title announces itself the first time somebody plays it.
var titleNames = map[string]string{
	"0100ED9024EB8000": "Outbound",
	"0100E65002BB8000": "Stardew Valley",
}

func titleName(id string) string {
	if n, ok := titleNames[id]; ok {
		return n
	}
	return id
}

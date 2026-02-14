package models

var ModelRegistry = []any{
	&Account{},
	&Transaction{},
	&LedgerEntry{},
}

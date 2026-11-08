package managed

import "github.com/CarterShi01/contexture-mcp-go/core/graph"

type GraphJournalProjection = graph.GraphJournalProjection
type GraphJournalProjectionOptions = graph.GraphJournalProjectionOptions
type TransactionEnvelope = graph.TransactionEnvelope
type TransactionLimits = graph.TransactionLimits
type PurePreparedTransaction = graph.PurePreparedTransaction
type TransactionPrepareOptions = graph.PrepareOptions
type History = graph.History
type RevisionRecord = graph.RevisionRecord

func DefaultTransactionLimits() TransactionLimits { return graph.DefaultTransactionLimits() }
func NewTransactionEnvelope(value any, limits TransactionLimits) (TransactionEnvelope, error) {
	return graph.NewTransactionEnvelope(value, limits)
}
func NewGraphJournalProjection(records []map[string]any, options GraphJournalProjectionOptions) (*GraphJournalProjection, error) {
	return graph.NewGraphJournalProjection(records, options)
}
func ProjectGraphJournal(records []map[string]any, options GraphJournalProjectionOptions) (FrozenJSON, error) {
	return graph.ProjectGraphJournal(records, options)
}
func PrepareTransaction(base RevisionState, envelope any, options TransactionPrepareOptions) (PurePreparedTransaction, error) {
	return graph.Prepare(base, envelope, options)
}
func RecheckPreparedTransaction(current RevisionState, prepared PurePreparedTransaction, options TransactionPrepareOptions) (PurePreparedTransaction, error) {
	return graph.RecheckPrepared(current, prepared, options)
}

package migrate

import (
	"testing"

	"entgo.io/ent/dialect/entsql"
	entschema "entgo.io/ent/dialect/sql/schema"
	"github.com/stretchr/testify/require"
)

func TestCompositeModelRouteSchemaMatchesAuthoritativeSQLConstraints(t *testing.T) {
	fk := findForeignKeyBySymbol(t, CompositeModelRoutesTable, "composite_model_routes_groups_group")
	require.Equal(t, entschema.Cascade, fk.OnDelete)

	idx := findIndexByName(t, CompositeModelRoutesTable, "idx_composite_model_routes_unique_active")
	require.True(t, idx.Unique)
	require.Equal(
		t,
		[]string{"group_id", "endpoint", "match_type", "public_model"},
		[]string{idx.Columns[0].Name, idx.Columns[1].Name, idx.Columns[2].Name, idx.Columns[3].Name},
	)
	require.NotNil(t, idx.Annotation)
	require.Equal(t, (&entsql.IndexAnnotation{Where: "deleted_at IS NULL"}).Where, idx.Annotation.Where)
}

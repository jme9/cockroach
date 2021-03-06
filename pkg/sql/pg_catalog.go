// Copyright 2016 The Cockroach Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
// implied. See the License for the specific language governing
// permissions and limitations under the License.
//
// Author: Nathan VanBenschoten (nvanbenschoten@gmail.com)

package sql

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash"
	"hash/fnv"
	"reflect"
	"strconv"

	"github.com/cockroachdb/cockroach/pkg/sql/parser"
	"github.com/cockroachdb/cockroach/pkg/sql/sqlbase"
	"github.com/pkg/errors"
)

var (
	oidZero   = parser.NewDInt(0)
	zeroVal   = oidZero
	negOneVal = parser.NewDInt(-1)
)

// pgCatalog contains a set of system tables mirroring PostgreSQL's pg_catalog schema.
// This code attempts to comply as closely as possible to the system catalogs documented
// in https://www.postgresql.org/docs/9.6/static/catalogs.html.
var pgCatalog = virtualSchema{
	name: "pg_catalog",
	tables: []virtualSchemaTable{
		pgCatalogAttrDefTable,
		pgCatalogAttributeTable,
		pgCatalogClassTable,
		pgCatalogConstraintTable,
		pgCatalogIndexesTable,
		pgCatalogNamespaceTable,
		pgCatalogTablesTable,
		pgCatalogTypeTable,
	},
}

// See: https://www.postgresql.org/docs/9.6/static/catalog-pg-attrdef.html.
var pgCatalogAttrDefTable = virtualSchemaTable{
	schema: `
CREATE TABLE pg_catalog.pg_attrdef (
	oid INT,
	adrelid INT,
	adnum INT,
	adbin STRING,
	adsrc STRING
);
`,
	populate: func(p *planner, addRow func(...parser.Datum) error) error {
		h := makeOidHasher()
		return forEachTableDesc(p,
			func(db *sqlbase.DatabaseDescriptor, table *sqlbase.TableDescriptor) error {
				colNum := 0
				return forEachColumnInTable(table, func(column *sqlbase.ColumnDescriptor) error {
					colNum++
					if column.DefaultExpr == nil {
						// pg_attrdef only expects rows for columns with default values.
						return nil
					}
					defSrc := parser.NewDString(*column.DefaultExpr)
					return addRow(
						h.ColumnOid(db, table, column),      // oid
						h.TableOid(db, table),               // adrelid
						parser.NewDInt(parser.DInt(colNum)), // adnum
						defSrc, // adbin
						defSrc, // adsrc
					)
				})
			},
		)
	},
}

// See: https://www.postgresql.org/docs/9.6/static/catalog-pg-attribute.html.
var pgCatalogAttributeTable = virtualSchemaTable{
	schema: `
CREATE TABLE pg_catalog.pg_attribute (
	attrelid INT,
	attname STRING,
	atttypid INT,
	attstattarget INT,
	attlen INT,
	attnum INT,
	attndims INT,
	attcacheoff INT,
	atttypmod INT,
	attbyval BOOL,
	attstorage CHAR,
	attalign CHAR,
	attnotnull BOOL,
	atthasdef BOOL,
	attisdropped BOOL,
	attislocal BOOL,
	attinhcount INT,
	attacl STRING,
	attoptions STRING,
	attfdwoptions STRING
);
`,
	populate: func(p *planner, addRow func(...parser.Datum) error) error {
		h := makeOidHasher()
		return forEachTableDesc(p,
			func(db *sqlbase.DatabaseDescriptor, table *sqlbase.TableDescriptor) error {
				// addColumn adds adds either a table or a index column to the pg_attribute table.
				addColumn := func(column *sqlbase.ColumnDescriptor, attRelID parser.Datum, colNum int) error {
					colTyp := column.Type.ToDatumType()
					return addRow(
						attRelID,                            // attrelid
						parser.NewDString(column.Name),      // attname
						typOid(colTyp),                      // atttypid
						zeroVal,                             // attstattarget
						typLen(colTyp),                      // attlen
						parser.NewDInt(parser.DInt(colNum)), // attnum
						zeroVal,      // attndims
						negOneVal,    // attcacheoff
						negOneVal,    // atttypmod
						parser.DNull, // attbyval (see pg_type.typbyval)
						parser.DNull, // attstorage
						parser.DNull, // attalign
						parser.MakeDBool(parser.DBool(!column.Nullable)),          // attnotnull
						parser.MakeDBool(parser.DBool(column.DefaultExpr != nil)), // atthasdef
						parser.MakeDBool(false),                                   // attisdropped
						parser.MakeDBool(true),                                    // attislocal
						zeroVal,                                                   // attinhcount
						parser.DNull,                                              // attacl
						parser.DNull,                                              // attoptions
						parser.DNull,                                              // attfdwoptions
					)
				}

				// Columns for table.
				colNum := 0
				if err := forEachColumnInTable(table, func(column *sqlbase.ColumnDescriptor) error {
					colNum++
					tableID := h.TableOid(db, table)
					return addColumn(column, tableID, colNum)
				}); err != nil {
					return err
				}

				// Columns for each index.
				return forEachIndexInTable(table, func(index *sqlbase.IndexDescriptor) error {
					colNum := 0
					return forEachColumnInIndex(table, index,
						func(column *sqlbase.ColumnDescriptor) error {
							colNum++
							idxID := h.IndexOid(db, table, index)
							return addColumn(column, idxID, colNum)
						},
					)
				})
			},
		)
	},
}

var (
	relKindTable = parser.NewDString("r")
	relKindIndex = parser.NewDString("i")
	relKindView  = parser.NewDString("v")
)

// See: https://www.postgresql.org/docs/9.6/static/catalog-pg-class.html.
var pgCatalogClassTable = virtualSchemaTable{
	schema: `
CREATE TABLE pg_catalog.pg_class (
	oid INT,
	relname STRING NOT NULL DEFAULT '',
	relnamespace INT,
	reltype INT,
	relowner INT,
	relam INT,
	relfilenode INT,
	reltablespace INT,
	relpages INT,
	reltuples FLOAT,
	relallvisible INT,
	reltoastrelid INT,
	relhasindex BOOL,
	relisshared BOOL,
	relistemp BOOL,
	relkind CHAR,
	relnatts INT,
	relchecks INT,
	relhasoids BOOL,
	relhaspkey BOOL,
	relhasrules BOOL,
	relhastriggers BOOL,
	relhassubclass BOOL,
	relfrozenxid INT,
	relacl STRING,
	reloptions STRING
);
`,
	populate: func(p *planner, addRow func(...parser.Datum) error) error {
		h := makeOidHasher()
		return forEachTableDesc(p,
			func(db *sqlbase.DatabaseDescriptor, table *sqlbase.TableDescriptor) error {
				// Table.
				relKind := relKindTable
				if table.IsView() {
					// The only difference between tables and views is the relkind column.
					relKind = relKindView
				}
				if err := addRow(
					h.TableOid(db, table),         // oid
					parser.NewDString(table.Name), // relname
					h.DBOid(db),                   // relnamespace
					oidZero,                       // reltype (PG creates a composite type in pg_type for each table)
					parser.DNull,                  // relowner
					parser.DNull,                  // relam
					oidZero,                       // relfilenode
					oidZero,                       // reltablespace
					parser.DNull,                  // relpages
					parser.DNull,                  // reltuples
					oidZero,                       // relallvisible
					oidZero,                       // reltoastrelid
					parser.MakeDBool(parser.DBool(table.IsPhysicalTable())), // relhasindex
					parser.MakeDBool(false),                                 // relisshared
					parser.MakeDBool(false),                                 // relistemp
					relKind,                                                 // relkind
					parser.NewDInt(parser.DInt(len(table.Columns))),         // relnatts
					parser.NewDInt(parser.DInt(len(table.Checks))),          // relchecks
					parser.MakeDBool(false),                                 // relhasoids
					parser.MakeDBool(parser.DBool(table.IsPhysicalTable())), // relhaspkey
					parser.MakeDBool(false),                                 // relhasrules
					parser.MakeDBool(false),                                 // relhastriggers
					parser.MakeDBool(false),                                 // relhassubclass
					zeroVal,                                                 // relfrozenxid
					parser.DNull,                                            // relacl
					parser.DNull,                                            // reloptions
				); err != nil {
					return err
				}

				// Indexes.
				return forEachIndexInTable(table, func(index *sqlbase.IndexDescriptor) error {
					return addRow(
						h.IndexOid(db, table, index),  // oid
						parser.NewDString(index.Name), // relname
						h.DBOid(db),                   // relnamespace
						oidZero,                       // reltype
						parser.DNull,                  // relowner
						parser.DNull,                  // relam
						oidZero,                       // relfilenode
						oidZero,                       // reltablespace
						parser.DNull,                  // relpages
						parser.DNull,                  // reltuples
						oidZero,                       // relallvisible
						oidZero,                       // reltoastrelid
						parser.MakeDBool(false),       // relhasindex
						parser.MakeDBool(false),       // relisshared
						parser.MakeDBool(false),       // relistemp
						relKindIndex,                  // relkind
						parser.NewDInt(parser.DInt(len(index.ColumnNames))), // relnatts
						zeroVal,                 // relchecks
						parser.MakeDBool(false), // relhasoids
						parser.MakeDBool(false), // relhaspkey
						parser.MakeDBool(false), // relhasrules
						parser.MakeDBool(false), // relhastriggers
						parser.MakeDBool(false), // relhassubclass
						zeroVal,                 // relfrozenxid
						parser.DNull,            // relacl
						parser.DNull,            // reloptions
					)
				})
			},
		)
	},
}

var (
	conTypeCheck     = parser.NewDString("c")
	conTypeFK        = parser.NewDString("f")
	conTypePKey      = parser.NewDString("p")
	conTypeUnique    = parser.NewDString("u")
	conTypeTrigger   = parser.NewDString("t")
	conTypeExclusion = parser.NewDString("x")

	// Avoid unused warning for constants.
	_ = conTypeTrigger
	_ = conTypeExclusion

	fkActionNone       = parser.NewDString("a")
	fkActionRestrict   = parser.NewDString("r")
	fkActionCascade    = parser.NewDString("c")
	fkActionSetNull    = parser.NewDString("n")
	fkActionSetDefault = parser.NewDString("d")

	// Avoid unused warning for constants.
	_ = fkActionRestrict
	_ = fkActionCascade
	_ = fkActionSetNull
	_ = fkActionSetDefault

	fkMatchTypeFull    = parser.NewDString("f")
	fkMatchTypePartial = parser.NewDString("p")
	fkMatchTypeSimple  = parser.NewDString("s")

	// Avoid unused warning for constants.
	_ = fkMatchTypeFull
	_ = fkMatchTypePartial
)

// See: https://www.postgresql.org/docs/9.6/static/catalog-pg-constraint.html.
var pgCatalogConstraintTable = virtualSchemaTable{
	schema: `
CREATE TABLE pg_catalog.pg_constraint (
	oid INT,
	conname STRING,
	connamespace INT,
	contype STRING,
	condeferrable BOOL,
	condeferred BOOL,
	convalidated BOOL,
	conrelid INT,
	contypid INT,
	conindid INT,
	confrelid INT,
	confupdtype STRING,
	confdeltype STRING,
	confmatchtype STRING,
	conislocal BOOL,
	coninhcount INT,
	connoinherit BOOL,
	conkey STRING,
	confkey STRING,
	conpfeqop STRING,
	conppeqop STRING,
	conffeqop STRING,
	conexclop STRING,
	conbin STRING,
	consrc STRING
);
`,
	populate: func(p *planner, addRow func(...parser.Datum) error) error {
		h := makeOidHasher()
		return forEachTableDescWithTableLookup(p,
			func(
				db *sqlbase.DatabaseDescriptor,
				table *sqlbase.TableDescriptor,
				tableLookup tableLookupFn,
			) error {
				info, err := table.GetConstraintInfoWithLookup(func(id sqlbase.ID) (
					*sqlbase.TableDescriptor, error,
				) {
					if _, t := tableLookup(id); t != nil {
						return t, nil
					}
					return nil, errors.Errorf("could not find referenced table with ID %v", id)
				})
				if err != nil {
					return err
				}

				for name, c := range info {
					oid := parser.DNull
					contype := parser.DNull
					conindid := zeroVal
					confrelid := zeroVal
					confupdtype := parser.DNull
					confdeltype := parser.DNull
					confmatchtype := parser.DNull
					conkey := parser.DNull
					confkey := parser.DNull
					consrc := parser.DNull

					// Determine constraint kind-specific fields.
					switch c.Kind {
					case sqlbase.ConstraintTypePK:
						oid = h.PrimaryKeyConstraintOid(db, table, c.Index)
						contype = conTypePKey
						conindid = h.IndexOid(db, table, c.Index)
						conkey = colIDArrayToDatum(c.Index.ColumnIDs)

					case sqlbase.ConstraintTypeFK:
						referencedDB, _ := tableLookup(c.ReferencedTable.ID)
						if referencedDB == nil {
							panic(fmt.Sprintf("could not find database of %+v", c.ReferencedTable))
						}

						oid = h.ForeignKeyConstraintOid(db, table, c.FK)
						contype = conTypeFK
						conindid = h.IndexOid(referencedDB, c.ReferencedTable, c.ReferencedIndex)
						confrelid = h.TableOid(referencedDB, c.ReferencedTable)
						confupdtype = fkActionNone
						confdeltype = fkActionNone
						confmatchtype = fkMatchTypeSimple
						conkey = colIDArrayToDatum(c.Index.ColumnIDs)
						confkey = colIDArrayToDatum(c.ReferencedIndex.ColumnIDs)

					case sqlbase.ConstraintTypeUnique:
						oid = h.UniqueConstraintOid(db, table, c.Index)
						contype = conTypeUnique
						conindid = h.IndexOid(db, table, c.Index)
						conkey = colIDArrayToDatum(c.Index.ColumnIDs)

					case sqlbase.ConstraintTypeCheck:
						oid = h.CheckConstraintOid(db, table, c.CheckConstraint)
						contype = conTypeCheck
						// TODO(nvanbenschoten) We currently do not store the referenced columns for a check
						// constraint. We should add an array of column indexes to
						// sqlbase.TableDescriptor_CheckConstraint and use that here.
						consrc = parser.NewDString(c.Details)
					}

					if err := addRow(
						oid,                                            // oid
						dStringOrNull(name),                            // conname
						h.DBOid(db),                                    // connamespace
						contype,                                        // contype
						parser.MakeDBool(false),                        // condeferrable
						parser.MakeDBool(false),                        // condeferred
						parser.MakeDBool(parser.DBool(!c.Unvalidated)), // convalidated
						h.TableOid(db, table),                          // conrelid
						zeroVal,                                        // contypid
						conindid,                                       // conindid
						confrelid,                                      // confrelid
						confupdtype,                                    // confupdtype
						confdeltype,                                    // confdeltype
						confmatchtype,                                  // confmatchtype
						parser.MakeDBool(true),                         // conislocal
						zeroVal,                                        // coninhcount
						parser.MakeDBool(true),                         // connoinherit
						conkey,                                         // conkey
						confkey,                                        // confkey
						parser.DNull,                                   // conpfeqop
						parser.DNull,                                   // conppeqop
						parser.DNull,                                   // conffeqop
						parser.DNull,                                   // conexclop
						consrc,                                         // conbin
						consrc,                                         // consrc
					); err != nil {
						return err
					}
				}
				return nil
			},
		)
	},
}

// colIDArrayToDatum returns a mock int[] as a DString for a slice of ColumnIDs.
// TODO(nvanbenschoten) use real int arrays when they are supported.
func colIDArrayToDatum(arr []sqlbase.ColumnID) parser.Datum {
	if len(arr) == 0 {
		return parser.DNull
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, val := range arr {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.WriteString(strconv.Itoa(int(val)))
	}
	buf.WriteByte('}')
	return parser.NewDString(buf.String())
}

// See: https://www.postgresql.org/docs/9.6/static/view-pg-indexes.html.
var pgCatalogIndexesTable = virtualSchemaTable{
	schema: `
CREATE TABLE pg_catalog.pg_indexes (
	schemaname STRING,
	tablename STRING,
	indexname STRING,
	tablespace STRING,
	indexdef STRING
);
`,
	populate: func(p *planner, addRow func(...parser.Datum) error) error {
		return forEachTableDesc(p,
			func(db *sqlbase.DatabaseDescriptor, table *sqlbase.TableDescriptor) error {
				return forEachIndexInTable(table, func(index *sqlbase.IndexDescriptor) error {
					def, err := indexDefFromDescriptor(p, db, table, index)
					if err != nil {
						return err
					}
					return addRow(
						parser.NewDString(db.Name),    // schemaname
						parser.NewDString(table.Name), // tablename
						parser.NewDString(index.Name), // indexname
						parser.DNull,                  // tablespace
						parser.NewDString(def),        // indexdef
					)
				})
			},
		)
	},
}

// indexDefFromDescriptor creates an index definition (`CREATE INDEX ... ON (...)`) from
// and index descriptor by reconstructing a CreateIndex parser node and calling its
// String method.
func indexDefFromDescriptor(
	p *planner,
	db *sqlbase.DatabaseDescriptor,
	table *sqlbase.TableDescriptor,
	index *sqlbase.IndexDescriptor,
) (string, error) {
	indexDef := parser.CreateIndex{
		Name: parser.Name(index.Name),
		Table: parser.NormalizableTableName{
			TableNameReference: &parser.TableName{
				DatabaseName: parser.Name(db.Name),
				TableName:    parser.Name(table.Name),
			},
		},
		Unique:  index.Unique,
		Columns: make(parser.IndexElemList, len(index.ColumnNames)),
		Storing: make(parser.NameList, len(index.StoreColumnNames)),
	}
	for i, name := range index.ColumnNames {
		elem := parser.IndexElem{
			Column:    parser.Name(name),
			Direction: parser.Ascending,
		}
		if index.ColumnDirections[i] == sqlbase.IndexDescriptor_DESC {
			elem.Direction = parser.Descending
		}
		indexDef.Columns[i] = elem
	}
	for i, name := range index.StoreColumnNames {
		indexDef.Storing[i] = parser.Name(name)
	}
	if len(index.Interleave.Ancestors) > 0 {
		intl := index.Interleave
		parentTable, err := sqlbase.GetTableDescFromID(p.txn, intl.Ancestors[len(intl.Ancestors)-1].TableID)
		if err != nil {
			return "", err
		}
		var sharedPrefixLen int
		for _, ancestor := range intl.Ancestors {
			sharedPrefixLen += int(ancestor.SharedPrefixLen)
		}
		fields := index.ColumnNames[:sharedPrefixLen]
		intlDef := &parser.InterleaveDef{
			Parent: parser.NormalizableTableName{
				TableNameReference: &parser.TableName{
					TableName: parser.Name(parentTable.Name),
				},
			},
			Fields: make(parser.NameList, len(fields)),
		}
		for i, field := range fields {
			intlDef.Fields[i] = parser.Name(field)
		}
		indexDef.Interleave = intlDef
	}
	return indexDef.String(), nil
}

// See: https://www.postgresql.org/docs/9.6/static/catalog-pg-namespace.html.
var pgCatalogNamespaceTable = virtualSchemaTable{
	schema: `
CREATE TABLE pg_catalog.pg_namespace (
	oid INT,
	nspname STRING NOT NULL DEFAULT '',
	nspowner INT,
	aclitem STRING
);
`,
	populate: func(p *planner, addRow func(...parser.Datum) error) error {
		h := makeOidHasher()
		return forEachDatabaseDesc(p, func(db *sqlbase.DatabaseDescriptor) error {
			return addRow(
				h.DBOid(db),                // oid
				parser.NewDString(db.Name), // nspname
				parser.DNull,               // nspowner
				parser.DNull,               // aclitem
			)
		})
	},
}

// See: https://www.postgresql.org/docs/9.6/static/view-pg-tables.html.
var pgCatalogTablesTable = virtualSchemaTable{
	schema: `
CREATE TABLE pg_catalog.pg_tables (
	schemaname STRING,
	tablename STRING,
	tableowner STRING,
	tablespace STRING,
	hasindexes BOOL,
	hasrules BOOL,
	hastriggers BOOL,
	rowsecurity BOOL
);
`,
	populate: func(p *planner, addRow func(...parser.Datum) error) error {
		return forEachTableDesc(p,
			func(db *sqlbase.DatabaseDescriptor, table *sqlbase.TableDescriptor) error {
				if table.IsView() {
					return nil
				}
				return addRow(
					parser.NewDString(db.Name),    // schemaname
					parser.NewDString(table.Name), // tablename
					parser.DNull,                  // tableowner
					parser.DNull,                  // tablespace
					parser.MakeDBool(parser.DBool(table.IsPhysicalTable())), // hasindexes
					parser.MakeDBool(false),                                 // hasrules
					parser.MakeDBool(false),                                 // hastriggers
					parser.MakeDBool(false),                                 // rowsecurity
				)
			},
		)
	},
}

var (
	typTypeBase      = parser.NewDString("b")
	typTypeComposite = parser.NewDString("c")
	typTypeDomain    = parser.NewDString("d")
	typTypeEnum      = parser.NewDString("e")
	typTypePseudo    = parser.NewDString("p")
	typTypeRange     = parser.NewDString("r")

	// Avoid unused warning for constants.
	_ = typTypeComposite
	_ = typTypeDomain
	_ = typTypeEnum
	_ = typTypePseudo
	_ = typTypeRange

	// See https://www.postgresql.org/docs/9.6/static/catalog-pg-type.html#CATALOG-TYPCATEGORY-TABLE.
	typCategoryArray       = parser.NewDString("A")
	typCategoryBoolean     = parser.NewDString("B")
	typCategoryComposite   = parser.NewDString("C")
	typCategoryDateTime    = parser.NewDString("D")
	typCategoryEnum        = parser.NewDString("E")
	typCategoryGeometric   = parser.NewDString("G")
	typCategoryNetworkAddr = parser.NewDString("I")
	typCategoryNumeric     = parser.NewDString("N")
	typCategoryPseudo      = parser.NewDString("P")
	typCategoryRange       = parser.NewDString("R")
	typCategoryString      = parser.NewDString("S")
	typCategoryTimespan    = parser.NewDString("T")
	typCategoryUserDefined = parser.NewDString("U")
	typCategoryBitString   = parser.NewDString("V")
	typCategoryUnknown     = parser.NewDString("X")

	// Avoid unused warning for constants.
	_ = typCategoryArray
	_ = typCategoryComposite
	_ = typCategoryEnum
	_ = typCategoryGeometric
	_ = typCategoryNetworkAddr
	_ = typCategoryPseudo
	_ = typCategoryRange
	_ = typCategoryBitString
	_ = typCategoryUnknown

	typDelim = parser.NewDString(",")
)

// See: https://www.postgresql.org/docs/9.6/static/catalog-pg-type.html.
var pgCatalogTypeTable = virtualSchemaTable{
	schema: `
CREATE TABLE pg_catalog.pg_type (
	oid INT,
	typname STRING NOT NULL DEFAULT '',
	typnamespace INT,
	typowner INT,
	typlen INT,
	typbyval BOOL,
	typtype CHAR,
	typcategory CHAR,
	typispreferred BOOL,
	typisdefined BOOL,
	typdelim CHAR,
	typrelid INT,
	typelem INT,
	typarray INT,
	typinput INT,
	typoutput INT,
	typreceive INT,
	typsend INT,
	typmodin INT,
	typmodout INT,
	typanalyze INT,
	typalign CHAR,
	typstorage CHAR,
	typnotnull BOOL,
	typbasetype INT,
	typtypmod INT,
	typndims INT,
	typcollation INT,
	typdefaultbin STRING,
	typdefault STRING,
	typacl STRING
);
`,
	populate: func(p *planner, addRow func(...parser.Datum) error) error {
		for oid, typ := range oidToDatum {
			if err := addRow(
				parser.NewDInt(parser.DInt(oid)), // oid
				parser.NewDString(typ.String()),  // typname
				parser.DNull,                     // typnamespace
				parser.DNull,                     // typowner
				typLen(typ),                      // typlen
				typByVal(typ),                    // typbyval
				typTypeBase,                      // typtype
				typCategory(typ),                 // typcategory
				parser.MakeDBool(false),          // typispreferred
				parser.MakeDBool(true),           // typisdefined
				typDelim,                         // typdelim
				zeroVal,                          // typrelid
				zeroVal,                          // typelem
				zeroVal,                          // typarray

				// regproc references
				zeroVal, // typinput
				zeroVal, // typoutput
				zeroVal, // typreceive
				zeroVal, // typsend
				zeroVal, // typmodin
				zeroVal, // typmodout
				zeroVal, // typanalyze

				parser.DNull,            // typalign
				parser.DNull,            // typstorage
				parser.MakeDBool(false), // typnotnull
				zeroVal,                 // typbasetype
				negOneVal,               // typtypmod
				zeroVal,                 // typndims
				zeroVal,                 // typcollation
				parser.DNull,            // typdefaultbin
				parser.DNull,            // typdefault
				parser.DNull,            // typacl
			); err != nil {
				return err
			}
		}
		return nil
	},
}

// typOid is the only OID generation approach that does not use oidHasher, because
// object identifiers for types are not arbitrary, but instead need to be kept in
// sync with Postgres.
func typOid(typ parser.Type) *parser.DInt {
	oid, _ := DatumToOid(typ)
	return parser.NewDInt(parser.DInt(oid))
}

func typLen(typ parser.Type) *parser.DInt {
	if sz, variable := typ.Size(); !variable {
		return parser.NewDInt(parser.DInt(sz))
	}
	return negOneVal
}

func typByVal(typ parser.Type) parser.Datum {
	_, variable := typ.Size()
	return parser.MakeDBool(parser.DBool(!variable))
}

// This mapping should be kept sync with PG's categorization.
var datumToTypeCategory = map[reflect.Type]*parser.DString{
	reflect.TypeOf(parser.TypeBool):        typCategoryBoolean,
	reflect.TypeOf(parser.TypeBytes):       typCategoryUserDefined,
	reflect.TypeOf(parser.TypeDate):        typCategoryDateTime,
	reflect.TypeOf(parser.TypeFloat):       typCategoryNumeric,
	reflect.TypeOf(parser.TypeInt):         typCategoryNumeric,
	reflect.TypeOf(parser.TypeInterval):    typCategoryTimespan,
	reflect.TypeOf(parser.TypeDecimal):     typCategoryNumeric,
	reflect.TypeOf(parser.TypeString):      typCategoryString,
	reflect.TypeOf(parser.TypeTimestamp):   typCategoryDateTime,
	reflect.TypeOf(parser.TypeTimestampTZ): typCategoryDateTime,
}

func typCategory(typ parser.Type) parser.Datum {
	return datumToTypeCategory[reflect.TypeOf(typ)]
}

// oidHasher provides a consistent hashing mechanism for object identifiers in
// pg_catalog tables, allowing for reliable joins across tables.
//
// In Postgres, oids are physical properties of database objects which are
// sequentially generated and naturally unique across all objects. See:
// https://www.postgresql.org/docs/9.6/static/datatype-oid.html.
// Because Cockroach does not have an equivalent concept, we generate arbitrary
// fingerprints for database objects with the only requirements being that they
// are unique across all objects and that they are stable across accesses.
//
// The type has a few layers of methods:
// - write<go_type> methods write concrete types to the underlying running hash.
// - write<db_object> methods account for single database objects like TableDescriptors
//   or IndexDescriptors in the running hash. These methods aim to write information
//   that would uniquely fingerprint the object to the hash using the first layer of
//   methods.
// - <DB_Object>Oid methods use the second layer of methods to construct a unique
//   object identifier for the provided database object. This object identifier will
//   be returned as a *parser.DInt, and the running hash will be reset. These are the
//   only methods that are part of the oidHasher's external facing interface.
//
type oidHasher struct {
	h hash.Hash32
}

func makeOidHasher() oidHasher {
	return oidHasher{h: fnv.New32()}
}

func (h oidHasher) writeStr(s string) {
	if _, err := h.h.Write([]byte(s)); err != nil {
		panic(err)
	}
}

func (h oidHasher) writeUInt8(i uint8) {
	if err := binary.Write(h.h, binary.BigEndian, i); err != nil {
		panic(err)
	}
}

func (h oidHasher) writeUInt32(i uint32) {
	if err := binary.Write(h.h, binary.BigEndian, i); err != nil {
		panic(err)
	}
}

type oidTypeTag uint8

const (
	_ oidTypeTag = iota
	databaseTypeTag
	tableTypeTag
	indexTypeTag
	columnTypeTag
	checkConstraintTypeTag
	fkConstraintTypeTag
	pKeyConstraintTypeTag
	uniqueConstraintTypeTag
)

func (h oidHasher) writeTypeTag(tag oidTypeTag) {
	h.writeUInt8(uint8(tag))
}

func (h oidHasher) getOid() *parser.DInt {
	i := h.h.Sum32()
	h.h.Reset()
	return parser.NewDInt(parser.DInt(i))
}

func (h oidHasher) writeDB(db *sqlbase.DatabaseDescriptor) {
	h.writeUInt32(uint32(db.ID))
	h.writeStr(db.Name)
}

func (h oidHasher) writeTable(table *sqlbase.TableDescriptor) {
	h.writeUInt32(uint32(table.ID))
	h.writeStr(table.Name)
}

func (h oidHasher) writeIndex(index *sqlbase.IndexDescriptor) {
	h.writeUInt32(uint32(index.ID))
}

func (h oidHasher) writeColumn(column *sqlbase.ColumnDescriptor) {
	h.writeUInt32(uint32(column.ID))
	h.writeStr(column.Name)
}

func (h oidHasher) writeCheckConstraint(check *sqlbase.TableDescriptor_CheckConstraint) {
	h.writeStr(check.Name)
	h.writeStr(check.Expr)
}

func (h oidHasher) writeForeignKeyReference(fk *sqlbase.ForeignKeyReference) {
	h.writeUInt32(uint32(fk.Table))
	h.writeUInt32(uint32(fk.Index))
	h.writeStr(fk.Name)
}

func (h oidHasher) DBOid(db *sqlbase.DatabaseDescriptor) *parser.DInt {
	h.writeTypeTag(databaseTypeTag)
	h.writeDB(db)
	return h.getOid()
}

func (h oidHasher) TableOid(
	db *sqlbase.DatabaseDescriptor, table *sqlbase.TableDescriptor,
) *parser.DInt {
	h.writeTypeTag(tableTypeTag)
	h.writeDB(db)
	h.writeTable(table)
	return h.getOid()
}

func (h oidHasher) IndexOid(
	db *sqlbase.DatabaseDescriptor, table *sqlbase.TableDescriptor, index *sqlbase.IndexDescriptor,
) *parser.DInt {
	h.writeTypeTag(indexTypeTag)
	h.writeDB(db)
	h.writeTable(table)
	h.writeIndex(index)
	return h.getOid()
}

func (h oidHasher) ColumnOid(
	db *sqlbase.DatabaseDescriptor, table *sqlbase.TableDescriptor, column *sqlbase.ColumnDescriptor,
) *parser.DInt {
	h.writeTypeTag(columnTypeTag)
	h.writeDB(db)
	h.writeTable(table)
	h.writeColumn(column)
	return h.getOid()
}

func (h oidHasher) CheckConstraintOid(
	db *sqlbase.DatabaseDescriptor,
	table *sqlbase.TableDescriptor,
	check *sqlbase.TableDescriptor_CheckConstraint,
) *parser.DInt {
	h.writeTypeTag(checkConstraintTypeTag)
	h.writeDB(db)
	h.writeTable(table)
	h.writeCheckConstraint(check)
	return h.getOid()
}

func (h oidHasher) PrimaryKeyConstraintOid(
	db *sqlbase.DatabaseDescriptor, table *sqlbase.TableDescriptor, pkey *sqlbase.IndexDescriptor,
) *parser.DInt {
	h.writeTypeTag(pKeyConstraintTypeTag)
	h.writeDB(db)
	h.writeTable(table)
	h.writeIndex(pkey)
	return h.getOid()
}

func (h oidHasher) ForeignKeyConstraintOid(
	db *sqlbase.DatabaseDescriptor, table *sqlbase.TableDescriptor, fk *sqlbase.ForeignKeyReference,
) *parser.DInt {
	h.writeTypeTag(fkConstraintTypeTag)
	h.writeDB(db)
	h.writeTable(table)
	h.writeForeignKeyReference(fk)
	return h.getOid()
}

func (h oidHasher) UniqueConstraintOid(
	db *sqlbase.DatabaseDescriptor, table *sqlbase.TableDescriptor, index *sqlbase.IndexDescriptor,
) *parser.DInt {
	h.writeTypeTag(uniqueConstraintTypeTag)
	h.writeDB(db)
	h.writeTable(table)
	h.writeIndex(index)
	return h.getOid()
}

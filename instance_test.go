package tengo

import (
	"database/sql"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
)

func TestNewInstance(t *testing.T) {
	assertError := func(driver, dsn string) {
		instance, err := NewInstance(driver, dsn)
		if instance != nil || err == nil {
			t.Errorf("Expected NewInstance(\"%s\", \"%s\") to return nil,err; instead found %v, %v", driver, dsn, instance, err)
		}
	}
	assertError("btrieve", "username:password@tcp(some.host)/dbname?param=value")
	assertError("", "username:password@tcp(some.host:1234)/dbname?param=value")
	assertError("mysql", "username:password@tcp(some.host:1234) i like zebras")

	assertInstance := func(dsn string, expectedInstance Instance) {
		expectedInstance.connectionPool = make(map[string]*sqlx.DB)
		instance, err := NewInstance("mysql", dsn)
		if err != nil {
			t.Fatalf("Unexpectedly received error %s from NewInstance(\"mysql\", \"%s\")", err, dsn)
		}
		expectedInstance.RWMutex = instance.RWMutex // cheat to satisfy DeepEqual
		if !reflect.DeepEqual(expectedInstance, *instance) {
			t.Errorf("NewInstance(\"mysql\", \"%s\"): Returned instance %#v does not match expected instance %#v", dsn, *instance, expectedInstance)
		}
	}

	dsn := "username:password@tcp(some.host:1234)/dbname"
	expected := Instance{
		BaseDSN:       "username:password@tcp(some.host:1234)/",
		Driver:        "mysql",
		User:          "username",
		Password:      "password",
		Host:          "some.host",
		Port:          1234,
		defaultParams: map[string]string{},
	}
	assertInstance(dsn, expected)

	dsn = "username:password@tcp(1.2.3.4:3306)/?param1=value1&readTimeout=5s&interpolateParams=0"
	expected = Instance{
		BaseDSN:  "username:password@tcp(1.2.3.4:3306)/",
		Driver:   "mysql",
		User:     "username",
		Password: "password",
		Host:     "1.2.3.4",
		Port:     3306,
		defaultParams: map[string]string{
			"param1":            "value1",
			"readTimeout":       "5s",
			"interpolateParams": "0",
		},
	}
	assertInstance(dsn, expected)

	dsn = "root@unix(/var/lib/mysql/mysql.sock)/dbname?param1=value1"
	expected = Instance{
		BaseDSN:    "root@unix(/var/lib/mysql/mysql.sock)/",
		Driver:     "mysql",
		User:       "root",
		Host:       "localhost",
		SocketPath: "/var/lib/mysql/mysql.sock",
		defaultParams: map[string]string{
			"param1": "value1",
		},
	}
	assertInstance(dsn, expected)
}

func TestInstanceBuildParamString(t *testing.T) {
	assertParamString := func(defaultOptions, addOptions, expectOptions string) {
		dsn := "username:password@tcp(1.2.3.4:3306)/"
		if defaultOptions != "" {
			dsn += "?" + defaultOptions
		}
		instance, err := NewInstance("mysql", dsn)
		if err != nil {
			t.Fatalf("NewInstance(\"mysql\", \"%s\") returned error: %s", dsn, err)
		}

		// can't compare strings directly since order may be different
		result := instance.buildParamString(addOptions)
		parsedResult, err := url.ParseQuery(result)
		if err != nil {
			t.Fatalf("url.ParseQuery(\"%s\") returned error: %s", result, err)
		}
		parsedExpected, err := url.ParseQuery(expectOptions)
		if err != nil {
			t.Fatalf("url.ParseQuery(\"%s\") returned error: %s", expectOptions, err)
		}
		if !reflect.DeepEqual(parsedResult, parsedExpected) {
			t.Errorf("Expected param map %v, instead found %v", parsedExpected, parsedResult)
		}
	}

	assertParamString("", "", "")
	assertParamString("param1=value1", "", "param1=value1")
	assertParamString("", "param1=value1", "param1=value1")
	assertParamString("param1=value1", "param1=value1", "param1=value1")
	assertParamString("param1=value1", "param1=hello", "param1=hello")
	assertParamString("param1=value1&readTimeout=5s&interpolateParams=0", "param2=value2", "param1=value1&readTimeout=5s&interpolateParams=0&param2=value2")
	assertParamString("param1=value1&readTimeout=5s&interpolateParams=0", "param1=value3", "param1=value3&readTimeout=5s&interpolateParams=0")
}

func (s TengoIntegrationSuite) TestInstanceConnect(t *testing.T) {
	// Connecting to invalid schema should return an error
	db, err := s.d.Connect("does-not-exist", "")
	if err == nil {
		t.Error("err is unexpectedly nil")
	} else if db != nil {
		t.Error("db is unexpectedly non-nil")
	}

	// Connecting without specifying a default schema should be successful
	db, err = s.d.Connect("", "")
	if err != nil {
		t.Errorf("Unexpected connection error: %s", err)
	} else if db == nil {
		t.Error("db is unexpectedly nil")
	}

	// Connecting again with same schema and params should return the existing connection pool
	db2, err := s.d.Connect("", "")
	if err != nil {
		t.Errorf("Unexpected connection error: %s", err)
	} else if db2 != db {
		t.Errorf("Expected same DB pool to be returned from identical Connect call; instead db=%v and db2=%v", db, db2)
	}

	// Connecting again with different schema should return a different connection pool
	db3, err := s.d.Connect("information_schema", "")
	if err != nil {
		t.Errorf("Unexpected connection error: %s", err)
	} else if db3 == db {
		t.Error("Expected different DB pool to be returned from Connect with different default db; instead was same")
	}

	// Connecting again with different params should return a different connection pool
	db4, err := s.d.Connect("information_schema", "foreign_key_checks=0&wait_timeout=20")
	if err != nil {
		t.Errorf("Unexpected connection error: %s", err)
	} else if db4 == db || db4 == db3 {
		t.Error("Expected different DB pool to be returned from Connect with different params; instead was same")
	}
}

func (s TengoIntegrationSuite) TestInstanceCanConnect(t *testing.T) {
	// Force a connection that has defaultParams
	dsn := fmt.Sprintf("%s?wait_timeout=5&timeout=1s", s.d.DSN())
	inst, err := NewInstance("mysql", dsn)
	if err != nil {
		t.Fatalf("Unexpected error from NewInstance: %s", err)
	}

	if ok, err := inst.CanConnect(); !ok || err != nil {
		t.Fatalf("Unexpected return from CanConnect(): %t / %s", ok, err)
	}

	// Stop the DockerizedInstance and confirm CanConnect result matches
	// expectation
	if err := s.d.Stop(); err != nil {
		t.Fatalf("Failed to Stop instance: %s", err)
	}
	ok, connErr := inst.CanConnect()
	if err := s.d.Start(); err != nil {
		t.Fatalf("Failed to re-Start() instance: %s", err)
	}
	if err := s.d.TryConnect(); err != nil {
		t.Fatalf("Failed to reconnect after restarting instance: %s", err)
	}
	if ok || connErr == nil {
		t.Errorf("Unexpected return from CanConnect(): %t / %s", ok, connErr)
	}
}

func (s TengoIntegrationSuite) TestInstanceCloseAll(t *testing.T) {
	makePool := func(defaultSchema, params string) {
		t.Helper()
		db, err := s.d.Connect(defaultSchema, params)
		if err != nil {
			t.Fatalf("Unexpected connection error: %s", err)
		} else if db == nil {
			t.Fatal("db is unexpectedly nil")
		}
	}
	assertPoolCount := func(expected int) {
		t.Helper()
		if actual := len(s.d.Instance.connectionPool); actual != expected {
			t.Errorf("Expected instance to have %d connection pools; instead found %d", expected, actual)
		}
	}

	s.d.CloseAll()
	assertPoolCount(0)
	makePool("", "")
	makePool("information_schema", "")
	assertPoolCount(2)
	s.d.CloseAll()
	assertPoolCount(0)
	makePool("", "")
	assertPoolCount(1)
}

func (s TengoIntegrationSuite) TestInstanceFlavorVersion(t *testing.T) {
	imageToFlavor := map[string]Flavor{
		"mysql:5.5":    FlavorMySQL55,
		"mysql:5.6":    FlavorMySQL56,
		"mysql:5.7":    FlavorMySQL57,
		"mysql:8.0":    FlavorMySQL80,
		"percona:5.5":  FlavorPercona55,
		"percona:5.6":  FlavorPercona56,
		"percona:5.7":  FlavorPercona57,
		"percona:8.0":  FlavorPercona80,
		"mariadb:10.1": FlavorMariaDB101,
		"mariadb:10.2": FlavorMariaDB102,
		"mariadb:10.3": FlavorMariaDB103,
		"mariadb:10.4": FlavorMariaDB104,
	}

	// Determine expected Flavor value of the Dockerized instance being tested
	var expected Flavor
	if result, ok := imageToFlavor[s.d.Image]; ok {
		expected = result
	} else {
		for image, result := range imageToFlavor {
			tokens := strings.SplitN(image, ":", 2)
			if len(tokens) < 2 {
				continue
			}
			repository, tag := tokens[0], tokens[1]
			if strings.Contains(s.d.Image, repository) && strings.Contains(s.d.Image, tag) {
				expected = result
				break
			}
		}
	}
	if expected == FlavorUnknown {
		t.Skip("No image map defined for", s.d.Image)
	}
	if actualFlavor := s.d.Flavor().Family(); actualFlavor != expected {
		t.Errorf("Expected image=%s to yield flavor=%s, instead found %s", s.d.Image, expected, actualFlavor)
	}
	if actualMajor, actualMinor, _ := s.d.Version(); actualMajor != expected.Major || actualMinor != expected.Minor {
		t.Errorf("Expected image=%s to yield major=%d minor=%d, instead found major=%d minor=%d", s.d.Image, expected.Major, expected.Minor, actualMajor, actualMinor)
	}

	// Confirm that SetFlavor does not work once flavor hydrated
	if err := s.d.SetFlavor(FlavorMariaDB102); err == nil {
		t.Error("Expected SetFlavor to return an error, but it was nil")
	}

	// Nuke the hydrated flavor, and confirm SetFlavor now works
	s.d.ForceFlavor(FlavorUnknown)
	if err := s.d.SetFlavor(expected); err != nil || s.d.Flavor() != expected {
		t.Errorf("Unexpected outcome from SetFlavor: error=%v, flavor=%s", err, s.d.Flavor())
	}
	s.d.ForceFlavor(FlavorUnknown) // Clean up
}

func (s TengoIntegrationSuite) TestInstanceCanSkipBinlog(t *testing.T) {
	// The dockerized instance in the test should always use root creds
	if !s.d.CanSkipBinlog() {
		t.Fatal("Expected all Dockerized instances to be able to skip binlogs, but CanSkipBinlogs returned false")
	}
	if _, err := s.d.Connect("", "sql_log_bin=0"); err != nil {
		t.Errorf("Error connecting with sql_log_bin=0: %v", err)
	}

	// Hack up the hydrated grants and confirm the method now returns false
	s.d.grants = []string{}
	if s.d.CanSkipBinlog() {
		t.Error("Expected empty grants to cause CanSkipBinlogs to return false, but it did not")
	}

	// Clean up previous hack, so that any future tests examining this field will
	// re-hydrate it
	s.d.grants = nil
}

func (s TengoIntegrationSuite) TestInstanceSchemas(t *testing.T) {
	// Currently at least 4 schemas in testdata/integration.sql
	schemas, err := s.d.Schemas()
	if err != nil || len(schemas) < 4 {
		t.Errorf("Expected at least 4 schemas, instead found %d, err=%s", len(schemas), err)
	}

	// Ensure SchemasByName is returning the same set of schemas
	byName, err := s.d.SchemasByName()
	if err != nil {
		t.Errorf("SchemasByName returned error: %s", err)
	} else if len(byName) != len(schemas) {
		t.Errorf("len(byName) != len(schemas): %d vs %d", len(byName), len(schemas))
	}
	seen := make(map[string]bool, len(byName))
	for _, schema := range schemas {
		if seen[schema.Name] {
			t.Errorf("Schema %s returned multiple times from call to instance.Schemas", schema.Name)
		}
		seen[schema.Name] = true
		if !reflect.DeepEqual(schema, byName[schema.Name]) {
			t.Errorf("Mismatch for schema %s between Schemas and SchemasByName", schema.Name)
		}
		if schema2, err := s.d.Schema(schema.Name); err != nil || !reflect.DeepEqual(schema2, schema) {
			t.Errorf("Mismatch for schema %s vs instance.Schema(%s); error=%s", schema.Name, schema.Name, err)
		}
		if has, err := s.d.HasSchema(schema.Name); !has || err != nil {
			t.Errorf("Expected HasSchema(%s)==true, instead found false", schema.Name)
		}
	}

	// Test SchemasByName with args
	byName, err = s.d.SchemasByName("testcharset", "doesnt_exist", "testcharcoll")
	if err != nil {
		t.Errorf("SchemasByName returned error: %s", err)
	}
	if len(byName) != 2 {
		t.Errorf("SchemasByName returned wrong number of results; expected 2, found %d", len(byName))
	}
	for name, schema := range byName {
		if name != schema.Name || (name != "testcharset" && name != "testcharcoll") {
			t.Errorf("SchemasByName returned mismatching schema: key=%s, name=%s", name, schema.Name)
		}
	}

	// Test negative responses
	if has, err := s.d.HasSchema("doesnt_exist"); has || err != nil {
		t.Error("HasSchema(doesnt_exist) unexpectedly returning true")
	}
	if schema, err := s.d.Schema("doesnt_exist"); schema != nil || err != sql.ErrNoRows {
		t.Errorf("Expected Schema(doesnt_exist) to return nil,sql.ErrNoRows; instead found %v,%s", schema, err)
	}
}

func (s TengoIntegrationSuite) TestInstanceShowCreateTable(t *testing.T) {
	t1create, err1 := s.d.ShowCreateTable("testing", "actor")
	t2create, err2 := s.d.ShowCreateTable("testing", "actor_in_film")
	if err1 != nil || err2 != nil || t1create == "" || t2create == "" {
		t.Fatalf("Unable to obtain SHOW CREATE TABLE output: err1=%s, err2=%s", err1, err2)
	}

	t1expected := aTableForFlavor(s.d.Flavor(), 1)
	if t1create != t1expected.CreateStatement {
		t.Errorf("Mismatch for SHOW CREATE TABLE\nActual return from %s:\n%s\n----------\nExpected output: %s", s.d.Image, t1create, t1expected.CreateStatement)
	}

	t2expected := anotherTableForFlavor(s.d.Flavor())
	if t2create != t2expected.CreateStatement {
		t.Errorf("Mismatch for SHOW CREATE TABLE\nActual return from %s:\n%s\n----------\nExpected output: %s", s.d.Image, t2create, t2expected.CreateStatement)
	}

	// Test nonexistent table
	t3create, err3 := s.d.ShowCreateTable("testing", "doesnt_exist")
	if t3create != "" || err3 == nil {
		t.Errorf("Expected ShowCreateTable on invalid table to return empty string and error, instead err=%s, output=%s", err3, t3create)
	}
}

func (s TengoIntegrationSuite) TestInstanceTableSize(t *testing.T) {
	size, err := s.d.TableSize("testing", "has_rows")
	if err != nil {
		t.Errorf("Error from TableSize: %s", err)
	} else if size < 1 {
		t.Errorf("TableSize returned a non-positive result: %d", size)
	}

	// Test nonexistent table
	size, err = s.d.TableSize("testing", "doesnt_exist")
	if size > 0 || err == nil {
		t.Errorf("Expected TableSize to return 0 size and non-nil err for missing table, instead size=%d and err=%s", size, err)
	}
}

func (s TengoIntegrationSuite) TestInstanceTableHasRows(t *testing.T) {
	if hasRows, err := s.d.TableHasRows("testing", "has_rows"); err != nil {
		t.Errorf("Error from TableHasRows: %s", err)
	} else if !hasRows {
		t.Error("Expected TableHasRows to return true for has_rows, instead returned false")
	}

	if hasRows, err := s.d.TableHasRows("testing", "no_rows"); err != nil {
		t.Errorf("Error from TableHasRows: %s", err)
	} else if hasRows {
		t.Error("Expected TableHasRows to return false for no_rows, instead returned true")
	}

	// Test nonexistent table
	if _, err := s.d.TableHasRows("testing", "doesnt_exist"); err == nil {
		t.Error("Expected TableHasRows to return error for nonexistent table, but it did not")
	}
}

func (s TengoIntegrationSuite) TestInstanceCreateSchema(t *testing.T) {
	opts := SchemaCreationOptions{
		DefaultCharSet:   "utf8mb4",
		DefaultCollation: "utf8mb4_unicode_ci",
		SkipBinlog:       true,
	}
	_, err := s.d.CreateSchema("foobar", opts)
	if err != nil {
		t.Fatalf("CreateSchema returned unexpected error: %s", err)
	}
	if refetch, err := s.d.Schema("foobar"); err != nil {
		t.Errorf("Unable to fetch newly created schema: %s", err)
	} else if refetch.CharSet != "utf8mb4" || refetch.Collation != "utf8mb4_unicode_ci" {
		t.Errorf("Unexpected charset or collation on refetched schema: %+v", refetch)
	}

	// Ensure creation of duplicate schema fails with error
	if _, err := s.d.CreateSchema("foobar", opts); err == nil {
		t.Error("Expected creation of duplicate schema to return an error, but it did not")
	}

	// Creation of schema without specifying charset and collation should use
	// instance defaults
	defCharSet, defCollation, err := s.d.DefaultCharSetAndCollation()
	if err != nil {
		t.Fatalf("Unable to obtain instance default charset and collation")
	}
	if schema, err := s.d.CreateSchema("barfoo", SchemaCreationOptions{}); err != nil {
		t.Errorf("Failed to create schema with default charset and collation: %s", err)
	} else if schema.CharSet != defCharSet || schema.Collation != defCollation {
		t.Errorf("Expected charset/collation to be %s/%s, instead found %s/%s", defCharSet, defCollation, schema.CharSet, schema.Collation)
	}
}

func (s TengoIntegrationSuite) TestInstanceDropSchema(t *testing.T) {
	opts := BulkDropOptions{
		MaxConcurrency:  10,
		OnlyIfEmpty:     true,
		PartitionsFirst: true,
	}
	// Dropping a schema with non-empty tables when OnlyIfEmpty==true should fail
	if err := s.d.DropSchema("testing", opts); err == nil {
		t.Error("Expected dropping a schema with tables to fail when OnlyIfEmpty==true, but it did not")
	}

	// Dropping a schema without tables when OnlyIfEmpty==true should succeed
	if err := s.d.DropSchema("testcollate", opts); err != nil {
		t.Errorf("Expected dropping a schema without tables to succeed when OnlyIfEmpty==true, but error=%s", err)
	}

	// Dropping a schema with only empty tables when OnlyIfEmpty==true should succeed
	if err := s.d.DropSchema("testcharcoll", opts); err != nil {
		t.Errorf("Expected dropping a schema with only empty tables to succeed when OnlyIfEmpty==true, but error=%s", err)
	}

	// Dropping a schema with non-empty tables when OnlyIfEmpty==false should succeed
	opts.OnlyIfEmpty = false
	if err := s.d.DropSchema("testing", opts); err != nil {
		t.Errorf("Expected dropping a schema with tables to succeed when OnlyIfEmpty==false, but error=%s", err)
	}

	// Dropping a schema that doesn't exist should fail
	if err := s.d.DropSchema("testing", opts); err == nil {
		t.Error("Expected dropping a nonexistent schema to fail, but error was nil")
	}
}

func (s TengoIntegrationSuite) TestInstanceDropTablesDeadlock(t *testing.T) {
	// With the new data dictionary, attempting to drop 2 tables concurrently can
	// deadlock if the tables have a foreign key constraint between them. This
	// deadlock did not occur in prior releases.
	if !s.d.Flavor().HasDataDictionary() {
		t.Skip("Test only relevant for flavors that have the new data dictionary")
	}

	db, err := s.d.Connect("", "foreign_key_checks=0")
	if err != nil {
		t.Fatalf("Unable to connect to DockerizedInstance: %s", err)
	}

	// Add a FK relation, drop all tables in the schema, and then restore the
	// test database to its previous state. Without the fix in DropTablesInSchema,
	// this tends to hit a deadlock within just a few loop iterations.
	opts := BulkDropOptions{MaxConcurrency: 10, SkipBinlog: true}
	for n := 0; n < 10; n++ {
		_, err = db.Exec("ALTER TABLE testing.actor_in_film ADD CONSTRAINT actor FOREIGN KEY (actor_id) REFERENCES testing.actor (actor_id)")
		if err != nil {
			t.Fatalf("Error running query on DockerizedInstance: %s", err)
		}
		if err = s.d.DropTablesInSchema("testing", opts); err != nil {
			t.Fatalf("Error dropping tables: %s", err)
		}
		if err = s.BeforeTest(""); err != nil {
			t.Fatalf("Error nuking and re-sourcing data: %s", err)
		}
	}
}

func (s TengoIntegrationSuite) TestInstanceDropRoutinesInSchema(t *testing.T) {
	// testing schema contains several routines in testdata/integration.sql
	schema := s.GetSchema(t, "testing")
	if len(schema.Routines) == 0 {
		t.Fatal("Assertion failure: schema `testing` has no routines to start")
	}
	opts := BulkDropOptions{
		MaxConcurrency: 10,
		SkipBinlog:     true,
	}
	if err := s.d.DropRoutinesInSchema("testing", opts); err != nil {
		t.Fatalf("Unexpected error from DropRoutinesInSchema: %v", err)
	}
	if schema = s.GetSchema(t, "testing"); len(schema.Routines) > 0 {
		t.Errorf("Expected schema `testing` to have no routines after DropRoutinesInSchema, instead found %d", len(schema.Routines))
	}

	// Repeated calls should have no effect, no error.
	if err := s.d.DropRoutinesInSchema("testing", opts); err != nil {
		t.Errorf("Unexpected error from DropRoutinesInSchema: %v", err)
	}

	// Calling on a nonexistent schema name should return an error.
	if err := s.d.DropRoutinesInSchema("doesntexist", opts); err == nil {
		t.Error("Expected error from DropRoutinesInSchema on nonexistent schema; instead err was nil")
	}
}

func (s TengoIntegrationSuite) TestInstanceAlterSchema(t *testing.T) {
	assertNoError := func(schemaName, newCharSet, newCollation, expectCharSet, expectCollation string) {
		t.Helper()
		opts := SchemaCreationOptions{
			DefaultCharSet:   newCharSet,
			DefaultCollation: newCollation,
		}
		if err := s.d.AlterSchema(schemaName, opts); err != nil {
			t.Errorf("Expected alter of %s to (%s,%s) would not error, but returned %s", schemaName, newCharSet, newCollation, err)
		} else {
			schema, err := s.d.Schema(schemaName)
			if err != nil {
				t.Fatalf("Unexpected error fetching schema: %s", err)
			}
			if schema.CharSet != expectCharSet {
				t.Errorf("Expected post-alter charset to be %s, instead found %s", expectCharSet, schema.CharSet)
			}
			if schema.Collation != expectCollation {
				t.Errorf("Expected post-alter collation to be %s, instead found %s", expectCollation, schema.Collation)
			}
		}
	}
	assertError := func(schemaName, newCharSet, newCollation string) {
		t.Helper()
		opts := SchemaCreationOptions{
			DefaultCharSet:   newCharSet,
			DefaultCollation: newCollation,
		}
		if err := s.d.AlterSchema(schemaName, opts); err == nil {
			t.Errorf("Expected alter of %s to (%s,%s) would return error, but returned nil instead", schemaName, newCharSet, newCollation)
		}
	}

	instCharSet, instCollation, err := s.d.DefaultCharSetAndCollation()
	if err != nil {
		t.Fatalf("Unable to fetch instance default charset and collation: %s", err)
	}

	// `testing` has instance-default charset and collation
	// `testcharset` has utf8mb4 charset with its default collation (utf8mb4_general_ci)
	// `testcharcoll` has utf8mb4 with utf8mb4_unicode_ci

	// Test no-op conditions
	assertNoError("testing", "", "", instCharSet, instCollation)
	assertNoError("testcharset", "utf8mb4", "", "utf8mb4", s.d.Flavor().DefaultUtf8mb4Collation())
	assertNoError("testcharset", "", "utf8mb4_general_ci", "utf8mb4", "utf8mb4_general_ci")
	assertNoError("testcharcoll", "utf8mb4", "utf8mb4_unicode_ci", "utf8mb4", "utf8mb4_unicode_ci")

	// Test known error conditions
	assertError("testing", "badcharset", "badcollation")    // charset and collation are invalid
	assertError("testcharset", "utf8", "latin1_swedish_ci") // charset and collation do not match
	assertError("nonexistent", "utf8mb4", "")               // schema does not actually exist in instance

	// Test successful alters
	assertNoError("testcharset", "", "utf8mb4_unicode_ci", "utf8mb4", "utf8mb4_unicode_ci")
	assertNoError("testcharcoll", "latin1", "", "latin1", "latin1_swedish_ci")
	assertNoError("testing", "utf8mb4", "utf8mb4_general_ci", "utf8mb4", "utf8mb4_general_ci")
}

func (s TengoIntegrationSuite) TestInstanceSchemaIntrospection(t *testing.T) {
	// Ensure our unit test fixtures and integration test fixtures match
	flavor := s.d.Flavor()
	schema, aTableFromDB := s.GetSchemaAndTable(t, "testing", "actor")
	aTableFromUnit := aTableForFlavor(flavor, 1)
	aTableFromUnit.CreateStatement = "" // Prevent diff from short-circuiting on equivalent CREATEs
	clauses, supported := aTableFromDB.Diff(&aTableFromUnit)
	if !supported {
		t.Error("Diff unexpectedly not supported for testing.actor")
	} else if len(clauses) > 0 {
		t.Errorf("Diff of testing.actor unexpectedly found %d clauses; expected 0. Clauses: %+v", len(clauses), clauses)
	}

	aTableFromDB = s.GetTable(t, "testing", "actor_in_film")
	aTableFromUnit = anotherTableForFlavor(flavor)
	aTableFromUnit.CreateStatement = "" // Prevent diff from short-circuiting on equivalent CREATEs
	clauses, supported = aTableFromDB.Diff(&aTableFromUnit)
	if !supported {
		t.Error("Diff unexpectedly not supported for testing.actor_in_film")
	} else if len(clauses) > 0 {
		t.Errorf("Diff of testing.actor_in_film unexpectedly found %d clauses; expected 0", len(clauses))
	}

	// ensure tables in testing schema are all supported (except where known not to be)
	for _, table := range schema.Tables {
		shouldBeUnsupported := (table.Name == unsupportedTable().Name)
		if table.UnsupportedDDL != shouldBeUnsupported {
			t.Errorf("Table %s: expected UnsupportedDDL==%v, instead found %v\nExpected SHOW CREATE TABLE:\n%s\nActual SHOW CREATE TABLE:\n%s", table.Name, shouldBeUnsupported, !shouldBeUnsupported, table.GeneratedCreateStatement(flavor), table.CreateStatement)
		}
	}

	// Test ObjectDefinitions map, which should contain objects of multiple types
	dict := schema.ObjectDefinitions()
	for key, create := range dict {
		var ok bool
		switch key.Type {
		case ObjectTypeTable:
			ok = strings.HasPrefix(create, "CREATE TABLE")
		case ObjectTypeProc, ObjectTypeFunc:
			ok = strings.HasPrefix(create, "CREATE DEFINER")
		}
		if !ok {
			t.Errorf("Unexpected or incorrect key %s found in schema object definitions --> %s", key, create)
		}
	}

	if dict[ObjectKey{Type: ObjectTypeFunc, Name: "func1"}] == "" || dict[ObjectKey{Type: ObjectTypeProc, Name: "func1"}] != "" {
		t.Error("ObjectDefinitions map not populated as expected")
	}

	// ensure character set handling works properly regardless of whether this
	// flavor has a data dictionary, which changed many SHOW CREATE TABLE behaviors
	schema = s.GetSchema(t, "testcharcoll")
	for _, table := range schema.Tables {
		if table.UnsupportedDDL {
			t.Errorf("Table %s unexpectedly not supported for diff.\nExpected SHOW CREATE TABLE:\n%s\nActual SHOW CREATE TABLE:\n%s", table.Name, table.GeneratedCreateStatement(flavor), table.CreateStatement)
		}
	}

	// Test various flavor-specific ordering fixes
	aTableFromDB = s.GetTable(t, "testing", "grab_bag")
	if aTableFromDB.UnsupportedDDL {
		t.Error("Cannot test various order-fixups because testing.grab_bag is unexpectedly not supported for diff")
	} else {
		// Test index order correction, even if no test image is using new data dict
		aTableFromDB.SecondaryIndexes[0], aTableFromDB.SecondaryIndexes[1], aTableFromDB.SecondaryIndexes[2] = aTableFromDB.SecondaryIndexes[2], aTableFromDB.SecondaryIndexes[0], aTableFromDB.SecondaryIndexes[1]
		fixIndexOrder(aTableFromDB)
		if aTableFromDB.GeneratedCreateStatement(flavor) != aTableFromDB.CreateStatement {
			t.Error("fixIndexOrder did not behave as expected")
		}

		// Test foreign key order correction, even if no test image lacks sorted FKs
		aTableFromDB.ForeignKeys[0], aTableFromDB.ForeignKeys[1], aTableFromDB.ForeignKeys[2] = aTableFromDB.ForeignKeys[2], aTableFromDB.ForeignKeys[0], aTableFromDB.ForeignKeys[1]
		fixForeignKeyOrder(aTableFromDB)
		if aTableFromDB.GeneratedCreateStatement(flavor) != aTableFromDB.CreateStatement {
			t.Error("fixForeignKeyOrder did not behave as expected")
		}

		// Test create option order correction, even if no test image is using new data dict
		aTableFromDB.CreateOptions = "ROW_FORMAT=COMPACT DELAY_KEY_WRITE=1 CHECKSUM=1"
		fixCreateOptionsOrder(aTableFromDB, flavor)
		if aTableFromDB.GeneratedCreateStatement(flavor) != aTableFromDB.CreateStatement {
			t.Error("fixCreateOptionsOrder did not behave as expected")
		}
	}

	// Test introspection of default expressions, if flavor supports them
	hasDefaultExpressions := flavor.VendorMinVersion(VendorMariaDB, 10, 2)
	if flavor.MySQLishMinVersion(8, 0) {
		if _, _, patch := s.d.Version(); patch >= 13 { // 8.0.13 added default expressions
			hasDefaultExpressions = true
		}
	}
	if hasDefaultExpressions {
		db, err := s.d.Connect("testing", "")
		if err != nil {
			t.Fatalf("Unexpected error from connect: %s", err)
		}
		if _, err := db.Exec("ALTER TABLE grab_bag ADD COLUMN expiration DATE DEFAULT (CURRENT_DATE + INTERVAL 1 YEAR)"); err != nil {
			t.Fatalf("Unexpected error from ALTER: %s", err)
		}
		table := s.GetTable(t, "testing", "grab_bag")
		if table.UnsupportedDDL {
			t.Error("Use of default expression unexpectedly triggers UnsupportedDDL")
		}
	}

	// Test introspection of generated columns, if flavor supports them
	if flavor.GeneratedColumns() {
		sqlfile := "testdata/generatedcols.sql"
		if flavor.Vendor == VendorMariaDB { // no support for NOT NULL generated cols
			sqlfile = "testdata/generatedcols-maria.sql"
		}
		if _, err := s.d.SourceSQL(sqlfile); err != nil {
			t.Fatalf("Unexpected error sourcing testdata/generatedcols.sql: %v", err)
		}
		table := s.GetTable(t, "testing", "staff")
		if table.UnsupportedDDL {
			t.Errorf("Expected table using generated columns to be supported for diff in flavor %s, but it was not.\nExpected SHOW CREATE TABLE:\n%s\nActual SHOW CREATE TABLE:\n%s", flavor, table.GeneratedCreateStatement(flavor), table.CreateStatement)
		}
		// Test generation expression fix, even if test image isn't MySQL 8
		for _, col := range table.Columns {
			if col.GenerationExpr != "" {
				col.GenerationExpr = "length(_latin1\\'fixme\\')"
			}
		}
		fixGenerationExpr(table, flavor)
		if table.GeneratedCreateStatement(flavor) != table.CreateStatement {
			t.Error("fixGenerationExpr did not behave as expected")
		}
	}

	// Test advanced index functionality in MySQL 8+
	if flavor.MySQLishMinVersion(8, 0) {
		if _, err := s.d.SourceSQL("testdata/index-mysql8.sql"); err != nil {
			t.Fatalf("Unexpected error sourcing testdata/index-mysql8.sql: %v", err)
		}
		table := s.GetTable(t, "testing", "my8idx")
		if table.UnsupportedDDL {
			t.Errorf("Expected table using advanced index functionality to be supported for diff in flavor %s, but it was not.\nExpected SHOW CREATE TABLE:\n%s\nActual SHOW CREATE TABLE:\n%s", flavor, table.GeneratedCreateStatement(flavor), table.CreateStatement)
		}
		idx := table.SecondaryIndexes[0]
		if !idx.Invisible {
			t.Errorf("Expected index %s to be invisible, but it was not", idx.Name)
		}
		if idx.Parts[0].Descending || !idx.Parts[1].Descending {
			t.Errorf("Unexpected index part collations found: [0].Descending=%t, [1].Descending=%t", idx.Parts[0].Descending, !idx.Parts[1].Descending)
		}
		if idx.Parts[0].Expression != "" || idx.Parts[1].Expression == "" {
			t.Errorf("Unexpected index part expressions found: [0].Expression=%q, [1].Expression=%q", idx.Parts[0].Expression, idx.Parts[1].Expression)
		}
	}

	// Test invisible column support in MariaDB 10.3+
	if flavor.VendorMinVersion(VendorMariaDB, 10, 3) {
		if _, err := s.d.SourceSQL("testdata/inviscols-maria.sql"); err != nil {
			t.Fatalf("Unexpected error sourcing testdata/inviscols-maria.sql: %v", err)
		}
		table := s.GetTable(t, "testing", "invistest")
		if table.UnsupportedDDL {
			t.Errorf("Expected table using invisible columns to be supported for diff in flavor %s, but it was not.\nExpected SHOW CREATE TABLE:\n%s\nActual SHOW CREATE TABLE:\n%s", flavor, table.GeneratedCreateStatement(flavor), table.CreateStatement)
		}
		for n, col := range table.Columns {
			expectInvis := (n == 0 || n == 4)
			if col.Invisible != expectInvis {
				t.Errorf("Expected Columns[%d].Invisible == %t, instead found %t", n, expectInvis, !expectInvis)
			}
		}
	}

	// Include coverage for fulltext parsers if MySQL 5.7+. (Although these are
	// supported in other flavors too, no alternative parsers ship with them.)
	if flavor.MySQLishMinVersion(5, 7) {
		if _, err := s.d.SourceSQL("testdata/ft-parser.sql"); err != nil {
			t.Fatalf("Unexpected error sourcing testdata/ft-parser.sql: %v", err)
		}
		table := s.GetTable(t, "testing", "ftparser")
		if table.UnsupportedDDL {
			t.Errorf("Expected table using ngram fulltext parser to be supported for diff in flavor %s, but it was not.\nExpected SHOW CREATE TABLE:\n%s\nActual SHOW CREATE TABLE:\n%s", flavor, table.GeneratedCreateStatement(flavor), table.CreateStatement)
		}
		indexes := table.SecondaryIndexesByName()
		if idx := indexes["ftdesc"]; idx.FullTextParser != "ngram" || idx.Type != "FULLTEXT" {
			t.Errorf("Expected index %s to be FULLTEXT with ngram parser, instead found type=%s / parser=%s", idx.Name, idx.Type, idx.FullTextParser)
		}
		if idx := indexes["ftbody"]; idx.FullTextParser != "" || idx.Type != "FULLTEXT" {
			t.Errorf("Expected index %s to be FULLTEXT with no parser, instead found type=%s / parser=%s", idx.Name, idx.Type, idx.FullTextParser)
		}
		if idx := indexes["name"]; idx.FullTextParser != "" || idx.Type != "BTREE" {
			t.Errorf("Expected index %s to be BTREE with no parser, instead found type=%s / parser=%s", idx.Name, idx.Type, idx.FullTextParser)
		}
	}
}

func (s TengoIntegrationSuite) TestInstanceRoutineIntrospection(t *testing.T) {
	schema := s.GetSchema(t, "testing")
	db, err := s.d.Connect("testing", "")
	if err != nil {
		t.Fatalf("Unexpected error from Connect: %s", err)
	}
	var sqlMode string
	if err = db.QueryRow("SELECT @@sql_mode").Scan(&sqlMode); err != nil {
		t.Fatalf("Unexpected error from Scan: %s", err)
	}

	procsByName := schema.ProceduresByName()
	actualProc1 := procsByName["proc1"]
	if actualProc1 == nil || len(procsByName) != 1 {
		t.Fatal("Unexpected result from ProceduresByName()")
	}
	expectProc1 := aProc(schema.Collation, sqlMode)
	if !expectProc1.Equals(actualProc1) {
		t.Errorf("Actual proc did not equal expected.\nACTUAL: %+v\nEXPECTED: %+v\n", actualProc1, &expectProc1)
	}

	funcsByName := schema.FunctionsByName()
	actualFunc1 := funcsByName["func1"]
	if actualFunc1 == nil || len(funcsByName) != 2 {
		t.Fatal("Unexpected result from FunctionsByName()")
	}
	expectFunc1 := aFunc(schema.Collation, sqlMode)
	if !expectFunc1.Equals(actualFunc1) {
		t.Errorf("Actual func did not equal expected.\nACTUAL: %+v\nEXPECTED: %+v\n", actualFunc1, &expectFunc1)
	}
	if actualFunc1.Equals(actualProc1) {
		t.Error("Equals not behaving as expected, proc1 and func1 should not be equal")
	}

	// If this flavor supports using mysql.proc to bulk-fetch routines, confirm
	// the result is identical to using the individual SHOW CREATE queries
	if !s.d.Flavor().HasDataDictionary() {
		fastResults, err := s.d.querySchemaRoutines("testing")
		if err != nil {
			t.Fatalf("Unexpected error from querySchemaRoutines: %s", err)
		}
		oldFlavor := s.d.Flavor()
		s.d.ForceFlavor(FlavorMySQL80)
		slowResults, err := s.d.querySchemaRoutines("testing")
		s.d.ForceFlavor(oldFlavor)
		if err != nil {
			t.Fatalf("Unexpected error from querySchemaRoutines: %s", err)
		}
		for n, r := range fastResults {
			if !r.Equals(slowResults[n]) {
				t.Errorf("Routine[%d] mismatch\nFast path value: %+v\nSlow path value: %+v\n", n, r, slowResults[n])
			}
		}
	}

	// Coverage for various nil cases and error conditions
	schema = nil
	if procCount := len(schema.ProceduresByName()); procCount != 0 {
		t.Errorf("nil schema unexpectedly contains %d procedures by name", procCount)
	}
	var r *Routine
	if actualFunc1.Equals(r) || !r.Equals(r) {
		t.Error("Equals not behaving as expected")
	}
	if _, err = showCreateRoutine(db, actualProc1.Name, ObjectTypeFunc); err != sql.ErrNoRows {
		t.Errorf("Unexpected error return from showCreateRoutine: expected sql.ErrNoRows, found %s", err)
	}
	if _, err = showCreateRoutine(db, actualFunc1.Name, ObjectTypeProc); err != sql.ErrNoRows {
		t.Errorf("Unexpected error return from showCreateRoutine: expected sql.ErrNoRows, found %s", err)
	}
	if _, err = showCreateRoutine(db, actualFunc1.Name, ObjectTypeTable); err == nil {
		t.Error("Expected non-nil error return from showCreateRoutine with invalid type, instead found nil")
	}
}

func (s TengoIntegrationSuite) TestInstanceStrictModeCompliant(t *testing.T) {
	assertCompliance := func(expected bool) {
		t.Helper()
		schemas, err := s.d.Schemas()
		if err != nil {
			t.Fatalf("Unexpected error from Schemas: %s", err)
		}
		compliant, err := s.d.StrictModeCompliant(schemas)
		if err != nil {
			t.Fatalf("Unexpected error from StrictModeCompliant: %s", err)
		}
		if compliant != expected {
			t.Errorf("Unexpected result from StrictModeCompliant: found %t", compliant)
		}
	}
	db, err := s.d.Connect("testing", "innodb_strict_mode=0&sql_mode=%27NO_ENGINE_SUBSTITUTION%27")
	if err != nil {
		t.Fatalf("Unexpected error from connect: %s", err)
	}
	exec := func(statement string) {
		t.Helper()
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("Unexpected error from Exec: %s", err)
		}
	}

	// Default setup in integration.sql is expected to be compliant, except in 5.5
	// due to use of zero default dates there only
	major, minor, _ := s.d.Version()
	expect := (major != 5 || minor != 5)
	assertCompliance(expect)

	if expect {
		// A table with a zero-date default should break compliance
		exec("CREATE TABLE has_zero_date (day date NOT NULL DEFAULT '0000-00-00')")
		assertCompliance(false)
		exec("DROP TABLE has_zero_date")
	} else {
		// 5.5 should become compliant if we drop the table with a zero-date default
		exec("DROP TABLE grab_bag")
		assertCompliance(true)
	}

	// Create tables with ROW_FORMAT=COMPRESSED. This should break compliance in
	// MySQL/Percona 5.5-5.6 and MariaDB 10.1, due to their default globals.
	exec("CREATE TABLE comprtest1 (name varchar(30)) ROW_FORMAT=COMPRESSED")
	exec("CREATE TABLE comprtest2 (name varchar(30)) ROW_FORMAT=COMPRESSED")
	expect = true
	if (major == 5 && minor <= 6) || s.d.Flavor().Family() == FlavorMariaDB101 {
		expect = false
	}
	assertCompliance(expect)
}

// TestFixColumnCompression confirms that CREATE TABLE parsing for Percona
// Server's compressed column feature works properly.
func TestFixColumnCompression(t *testing.T) {
	table := supportedTableForFlavor(FlavorPercona57)
	if table.Columns[3].Name != "metadata" || table.Columns[3].ColumnFormat != "" {
		t.Fatal("Test fixture has changed without corresponding update to this test's logic")
	}

	table.CreateStatement = strings.Replace(table.CreateStatement, "`metadata` text", "`metadata` text /*!50633 COLUMN_FORMAT COMPRESSED */", 1)
	fixColumnCompression(&table)
	if table.Columns[3].ColumnFormat != "COMPRESSED" {
		t.Errorf("Expected column's format to be %q, instead found %q", "COMPRESSED", table.Columns[3].ColumnFormat)
	}
	if table.GeneratedCreateStatement(FlavorPercona57) != table.CreateStatement {
		t.Errorf("Unexpected mismatch in generated CREATE TABLE:\nGeneratedCreateStatement:\n%s\nCreateStatement:\n%s", table.GeneratedCreateStatement(FlavorPercona57), table.CreateStatement)
	}

	table.CreateStatement = strings.Replace(table.CreateStatement, "COMPRESSED */", "COMPRESSED WITH COMPRESSION_DICTIONARY `foobar` */", 1)
	fixColumnCompression(&table)
	if table.Columns[3].ColumnFormat != "COMPRESSED WITH COMPRESSION_DICTIONARY `foobar`" {
		t.Errorf("Expected column's format to be %q, instead found %q", "COMPRESSED WITH COMPRESSION_DICTIONARY `foobar`", table.Columns[3].ColumnFormat)
	}
	if table.GeneratedCreateStatement(FlavorPercona57) != table.CreateStatement {
		t.Errorf("Unexpected mismatch in generated CREATE TABLE:\nGeneratedCreateStatement:\n%s\nCreateStatement:\n%s", table.GeneratedCreateStatement(FlavorPercona57), table.CreateStatement)
	}
}

// TestFixFulltextIndexParsers confirms CREATE TABLE parsing for WITH PARSER
// clauses works properly.
func TestFixFulltextIndexParsers(t *testing.T) {
	table := anotherTableForFlavor(FlavorMySQL57)
	if table.SecondaryIndexes[0].Type != "BTREE" || table.SecondaryIndexes[0].FullTextParser != "" {
		t.Fatal("Test fixture has changed without corresponding update to this test's logic")
	}

	// Confirm no parser = no change from fix
	table.SecondaryIndexes[0].Type = "FULLTEXT"
	table.CreateStatement = table.GeneratedCreateStatement(FlavorMySQL57)
	fixFulltextIndexParsers(&table, FlavorMySQL57)
	if table.SecondaryIndexes[0].FullTextParser != "" {
		t.Errorf("fixFulltextIndexParsers unexpectedly set parser to %q instead of %q", table.SecondaryIndexes[0].FullTextParser, "")
	}

	// Confirm parser extracted correctly from fix
	table.SecondaryIndexes[0].FullTextParser = "ngram"
	table.CreateStatement = table.GeneratedCreateStatement(FlavorMySQL57)
	table.SecondaryIndexes[0].FullTextParser = ""
	fixFulltextIndexParsers(&table, FlavorMySQL57)
	if table.SecondaryIndexes[0].FullTextParser != "ngram" {
		t.Errorf("fixFulltextIndexParsers unexpectedly set parser to %q instead of %q", table.SecondaryIndexes[0].FullTextParser, "ngram")
	}
}

package policy

import (
	"bytes"
	_ "embed"
	"fmt"

	v6 "github.com/santhosh-tekuri/jsonschema/v6"
)

// The machine-readable contracts of PRD 01. The Go types and these schemas
// describe the same documents; the conformance tests keep the two honest by
// validating real documents against the schemas and the schemas against each
// other's examples.
const (
	PolicySchemaID   = "https://openpak.org/schemas/v2/policy.schema.json"
	ManifestSchemaID = "https://openpak.org/schemas/v2/manifest.schema.json"
)

//go:embed schema/v2-policy.schema.json
var policySchemaSrc []byte

//go:embed schema/v2-manifest.schema.json
var manifestSchemaSrc []byte

// PolicySchemaSource returns the embedded policy schema bytes.
func PolicySchemaSource() []byte { return policySchemaSrc }

// ManifestSchemaSource returns the embedded manifest schema bytes.
func ManifestSchemaSource() []byte { return manifestSchemaSrc }

// newCompiler builds a schema compiler that resolves both embedded sources
// and asserts string formats (issued_at's date-time).
func newCompiler() (*v6.Compiler, error) {
	c := v6.NewCompiler()
	c.AssertFormat()
	for id, src := range map[string][]byte{
		PolicySchemaID:   policySchemaSrc,
		ManifestSchemaID: manifestSchemaSrc,
	} {
		doc, err := v6.UnmarshalJSON(bytes.NewReader(src))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", id, err)
		}
		if err := c.AddResource(id, doc); err != nil {
			return nil, fmt.Errorf("%s: %w", id, err)
		}
	}
	return c, nil
}

// CompileSchemas compiles both embedded schemas for conformance tests and
// CI gates. References resolve through the embedded sources only — a schema
// that references the network is a build failure, not a runtime surprise.
func CompileSchemas() (policySchema, manifestSchema *v6.Schema, err error) {
	c, err := newCompiler()
	if err != nil {
		return nil, nil, err
	}
	policySchema, err = c.Compile(PolicySchemaID)
	if err != nil {
		return nil, nil, fmt.Errorf("policy schema: %w", err)
	}
	manifestSchema, err = c.Compile(ManifestSchemaID)
	if err != nil {
		return nil, nil, fmt.Errorf("manifest schema: %w", err)
	}
	return policySchema, manifestSchema, nil
}

// validateAgainst compiles the embedded schemas once per call and validates
// data against the named one. The conformance tests are the callers; hot
// paths use the Go-native validation, which owns the same rules with better
// errors.
func validateAgainst(schemaID string, data []byte) error {
	c, err := newCompiler()
	if err != nil {
		return err
	}
	sch, err := c.Compile(schemaID)
	if err != nil {
		return fmt.Errorf("schema %s: %w", schemaID, err)
	}
	value, err := v6.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("document: %w", err)
	}
	if err := sch.Validate(value); err != nil {
		return fmt.Errorf("document: %w", err)
	}
	return nil
}

// ValidatePolicyAgainstSchema checks data against the embedded policy
// schema. This is the conformance view of the contract; Validate is the
// enforcement view.
func ValidatePolicyAgainstSchema(data []byte) error {
	return validateAgainst(PolicySchemaID, data)
}

// ValidateManifestAgainstSchema checks data against the embedded manifest
// schema.
func ValidateManifestAgainstSchema(data []byte) error {
	return validateAgainst(ManifestSchemaID, data)
}

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPlanAuditXml_Exists(t *testing.T) {
	readEmbeddedCommand(t, "plan-audit.xml")
}

func TestPlanAuditMd_Exists(t *testing.T) {
	readEmbeddedCommand(t, "plan-audit.md")
}

func TestPlanAuditXml_ContainsRubricNumbers(t *testing.T) {
	content := readEmbeddedCommand(t, "plan-audit.xml")
	assert.Contains(t, content, "−25")
	assert.Contains(t, content, "−15")
	assert.Contains(t, content, "−8")
	assert.Contains(t, content, "−3")
	assert.Contains(t, content, "90")
}

func TestPlanAuditXml_ContainsLedgerMandate(t *testing.T) {
	content := readEmbeddedCommand(t, "plan-audit.xml")
	assert.Contains(t, content, "re-verify")
	assert.Contains(t, content, "RESOLVED")
}

func TestPlanAuditXml_ContainsGuardrail(t *testing.T) {
	content := readEmbeddedCommand(t, "plan-audit.xml")
	assert.Contains(t, content, "reach 90% by fixing the plan, never by weakening validate/acceptance")
}

func TestPlanAuditXml_NoDanglingValidateRef(t *testing.T) {
	content := readEmbeddedCommand(t, "plan-audit.xml")
	assert.NotContains(t, content, "validate.xml step 2d")
}

package rules

// aggregate_triad_test.go is the BEHAVIORAL guard for the php-symfony
// aggregate-triad must-rules (PHP-PERS-007 / PHP-PERS-008 / PHP-ARCH-018). It
// loads the SHIPPED embedded YAML (not inline copies) so it catches drift
// between what these tests assert and what actually ships, expands the
// {src}/{infra} tokens for a single-root Symfony layout, and runs Check over two
// fixture trees: a CLASSIC aggregate (loose VOs held directly, scalar event
// payload, shared Id under a flat DataType/ root) must trip >=1 `must` rule,
// while a proper TRIAD (#[AggregateDAO] <X>Data DAO + readonly <X>DTO + readonly
// <X>EventRecord) must trip zero — including the false-positive trap where a
// legit nested <X>EventRecord legitimately holds scalars but sits outside the
// {src}/**/Event/*.php footprint.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// loadShippedPhpSymfonyRules reads the committed embedded persistence.yaml +
// architecture.yaml as bare []CodeRule, concatenates them, and expands the
// {src}/{infra} tokens for a single-root Symfony layout (SourceRoots: ["src"],
// InfraLayer: "Bundle"). Loading the SHIPPED files is deliberate: an inline copy
// could silently diverge from what the catalogue actually ships.
func loadShippedPhpSymfonyRules(t *testing.T) []CodeRule {
	t.Helper()
	base := filepath.Join("..", "..", "cmd", "tmux-cli", "embedded", "rules", "php-symfony")
	var all []CodeRule
	for _, name := range []string{"persistence.yaml", "architecture.yaml"} {
		data, err := os.ReadFile(filepath.Join(base, name))
		require.NoError(t, err, "shipped %s must be readable", name)
		var set []CodeRule
		require.NoError(t, yaml.Unmarshal(data, &set), "shipped %s must parse", name)
		require.NotEmpty(t, set, "shipped %s must contain rules", name)
		all = append(all, set...)
	}
	return ExpandAppliesTo(all, Layout{SourceRoots: []string{"src"}, InfraLayer: "Bundle"})
}

// mustViolations returns the ids of every `must`-severity rule reported Violated
// by a Check run — the behavioral contract the two tests assert on.
func mustViolations(res CheckResult) []string {
	var ids []string
	for _, r := range res.Rules {
		if r.Severity == "must" && r.Violated {
			ids = append(ids, r.ID)
		}
	}
	return ids
}

func TestPhpSymfonyAggregateTriad_ClassicFlagged(t *testing.T) {
	rules := loadShippedPhpSymfonyRules(t)
	dir := t.TempDir()

	// Classic aggregate: loose VOs promoted directly into the aggregate.
	writeFile(t, dir, "src/Domain/User/User.php", `<?php
namespace App\Domain\User;

final class User
{
    public function __construct(
        private EmailAddress $email,
        private FullName $fullName,
        private UserStatus $status,
    ) {}
}
`)
	// Classic event: scalar payload promoted into the constructor.
	writeFile(t, dir, "src/Domain/User/Event/UserRegistered.php", `<?php
namespace App\Domain\User\Event;

final class UserRegistered
{
    public function __construct(
        public readonly string $userId,
        public readonly string $email,
    ) {}
}
`)
	// Classic shared Id: parked under a flat DataType/ root.
	writeFile(t, dir, "src/DataType/UserId.php", `<?php
namespace App\DataType;

final class UserId extends AbstractIntId
{
}
`)

	files := []string{
		"src/Domain/User/User.php",
		"src/Domain/User/Event/UserRegistered.php",
		"src/DataType/UserId.php",
	}
	res := Check(rules, files, "domain", dir)

	violated := mustViolations(res)
	require.NotEmpty(t, violated, "a classic aggregate must trip >=1 must rule; got none")
	// All three triad must-rules fire on the classic fixture set.
	assert.Contains(t, violated, "PHP-PERS-007", "loose VO aggregate must trip PHP-PERS-007")
	assert.Contains(t, violated, "PHP-ARCH-018", "scalar event payload must trip PHP-ARCH-018")
	assert.Contains(t, violated, "PHP-PERS-008", "shared Id under flat DataType/ must trip PHP-PERS-008")
}

func TestPhpSymfonyAggregateTriad_TriadClean(t *testing.T) {
	rules := loadShippedPhpSymfonyRules(t)
	dir := t.TempDir()

	// Proper triad: aggregate holds a #[AggregateDAO] <X>Data DAO, exposed as a
	// readonly <X>DTO via getData().
	writeFile(t, dir, "src/Domain/User/User.php", `<?php
namespace App\Domain\User;

final class User
{
    public function __construct(
        private readonly UserId $id,
        #[AggregateDAO]
        private readonly UserData $data,
    ) {}

    public function getData(): UserDTO
    {
        return $this->data->toDTO();
    }
}
`)
	// The mutable persistence DAO holds primitives, not loose VOs.
	writeFile(t, dir, "src/Domain/User/UserData.php", `<?php
namespace App\Domain\User;

#[AggregateDAO]
final class UserData
{
    private string $email;
    private string $fullName;
    private int $status;
}
`)
	// The readonly input/output DTO.
	writeFile(t, dir, "src/Domain/User/UserDTO.php", `<?php
namespace App\Domain\User;

final class UserDTO
{
    public function __construct(
        public readonly string $email,
        public readonly string $fullName,
    ) {}
}
`)
	// The event carries a single readonly <X>EventRecord and implements the
	// aggregate's event interface.
	writeFile(t, dir, "src/Domain/User/Event/UserRegistered.php", `<?php
namespace App\Domain\User\Event;

final class UserRegistered implements UserEventInterface
{
    public function __construct(
        public readonly UserRegisteredEventRecord $record,
    ) {}
}
`)
	// The EventRecord legitimately holds scalars, but lives NESTED under
	// Event/Record/ so it is outside the {src}/**/Event/*.php footprint — the
	// false-positive trap PHP-ARCH-018 must not fire on.
	writeFile(t, dir, "src/Domain/User/Event/Record/UserRegisteredEventRecord.php", `<?php
namespace App\Domain\User\Event\Record;

final class UserRegisteredEventRecord
{
    public function __construct(
        public readonly string $userId,
        public readonly string $email,
    ) {}
}
`)
	// The shared Id lives beside its aggregate, not under a flat DataType/ root.
	writeFile(t, dir, "src/Domain/User/UserId.php", `<?php
namespace App\Domain\User;

final class UserId extends AbstractIntId
{
}
`)

	files := []string{
		"src/Domain/User/User.php",
		"src/Domain/User/UserData.php",
		"src/Domain/User/UserDTO.php",
		"src/Domain/User/Event/UserRegistered.php",
		"src/Domain/User/Event/Record/UserRegisteredEventRecord.php",
		"src/Domain/User/UserId.php",
	}
	res := Check(rules, files, "domain", dir)

	assert.Empty(t, mustViolations(res),
		"a proper aggregate triad must trip zero must rules (incl. the nested EventRecord false-positive trap)")
}

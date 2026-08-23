// Package verifypipeline holds the tests for the reusable verify workflow and
// the shell steps it runs.
//
// The decisions that must fail closed live in .github/scripts rather than in
// workflow expressions, because an expression can only be exercised by running
// a pipeline, and a gate nobody can run a failing case against is a gate
// nobody has proved. Each script is a small program with an exit code, and
// each test here drives it with the inputs a real run would produce.
//
// The package holds tests only. Nothing imports it.
package verifypipeline

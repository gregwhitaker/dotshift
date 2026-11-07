package agent

// GoalOutputSchema is shared by goal execution and provider compatibility checks.
const GoalOutputSchema = `{
  "type": "object",
  "properties": {
    "status": {"type": "string", "enum": ["continue", "complete", "blocked"]},
    "summary": {"type": "string"},
    "next_steps": {"type": "string"}
  },
  "required": ["status", "summary", "next_steps"],
  "additionalProperties": false
}`

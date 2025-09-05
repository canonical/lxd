# sharedapi.instructions.md
---
applyTo: "shared/api/*.go"
---

## Overview

The `shared/api` package contains Go structs for all LXD API objects. This package defines the data structures used in the REST API, providing consistent interfaces for object creation, modification, and retrieval across the LXD ecosystem.

## Purpose and Scope

This package serves as the contract between LXD components:
- **LXD daemon** uses these structs for API endpoints
- **LXC client** uses them for API communication  
- **Third-party clients** rely on these definitions for integration
- **Documentation generation** extracts API specs from these structs

## Struct Naming Conventions

### Base Naming Pattern
- **Primary structs**: Named after the API object (LXD entity) they represent (e.g., `Project`, `Profile`, `Instance`, `Network`)
- Use **PascalCase** and prefer full words over abbreviations
- Keep names concise but descriptive

### Operation-Specific Variants

#### POST Structs (Object Creation)
- **Collection endpoints**: `{Object}sPost` (plural form)
  - Example: `ProjectsPost` for creating projects
- **Individual operations**: `{Object}Post` (singular form)  
  - Example: `ProjectPost` for renaming projects
- Typically embed corresponding PUT structs using `yaml:",inline"`

#### PUT Structs (Object Updates)
- **Pattern**: `{Object}Put`
- Example: `ProjectPut`, `ProfilePut`
- Contain **only modifiable fields** (exclude read-only data)
- Should not include auto-generated or system-managed fields

#### GET Structs (Object Retrieval)
- The **base struct itself** serves as the GET response
- Includes both modifiable and read-only fields
- Contains complete object state

## Field Documentation Standards

### Comment Requirements
- **All exported fields** must have descriptive comments
- End comments with periods
- Include **practical examples** in comments using `// Example:` format
- Be specific and actionable in descriptions

### Example Format
```go
// Description of the project
// Example: My production environment
Description string `json:"description" yaml:"description"`

// Project configuration map (refer to doc/projects.md)  
// Example: {"features.profiles": "true", "features.networks": "false"}
Config map[string]string `json:"config" yaml:"config"`
```

### API Extension Annotations
- Document when features were introduced using `// API extension:` comments
- Helps with backward compatibility and feature detection
- Example: `// API extension: projects`

### Swagger Documentation
- Add `// swagger:model` annotations for API documentation generation
- Place directly above struct definitions
- Critical for REST API specification generation

## Common Field Types and Patterns

### Configuration Maps
- Use `map[string]string` for flexible configuration options
- Keys should follow established naming conventions (dot-separated)
- Provide meaningful examples in comments

### Object Relationships
- Use `UsedBy []string` fields to track what objects reference this one
- Contains API URLs pointing to dependent objects
- Mark as `// Read only: true` in comments

### Timestamps
- Use `time.Time` for all temporal data
- Common fields: `CreatedAt`, `LastUsedAt`
- Include timezone information implicitly (UTC expected)

### Boolean Flags
- Use descriptive names that read naturally
- Example: `Ephemeral bool` rather than `IsEphemeral bool`
- Document default behavior clearly

### String Constants
- Define typed string constants for enumerated values
- Example: `type InstanceType string` with constants like `InstanceTypeContainer`
- Improves type safety and API clarity

## Struct Tags Requirements

### Required Tags
All fields must include both JSON and YAML tags:
```go
Name string `json:"name" yaml:"name"`
```

### Read-Only Fields
Mark system-generated fields appropriately:
```go
// Read only: true  
CreatedAt time.Time `json:"created_at" yaml:"created_at"`
```

### Optional Database Tags
Include database constraints when relevant:
```go
Name string `json:"name" yaml:"name" db:"primary=yes"`
```

## Standard Methods

### Writable() Method
Convert full structs to PUT structs (filter read-only fields):
```go
func (obj *Object) Writable() ObjectPut {
    return ObjectPut{
        Description: obj.Description,
        Config:      obj.Config,
        // Include only modifiable fields
    }
}
```

### SetWritable() Method  
Apply PUT struct values to full structs:
```go
func (obj *Object) SetWritable(put ObjectPut) {
    obj.Description = put.Description
    obj.Config = put.Config
    // Update only modifiable fields
}
```

### URL() Method (when applicable)
Generate API URLs for objects:
```go
func (obj *Object) URL(apiVersion string, projectName string) *URL {
    return NewURL().Path(apiVersion, "objects", obj.Name).Project(projectName)
}
```

## API Extension Guidelines

### Tracking New Features
- Every new field or struct should reference its introducing API extension
- Helps with feature detection and backward compatibility
- Document in both comments and code

### Backward Compatibility
- Never remove or rename existing fields without deprecation
- Use `omitempty` JSON tags for optional fields
- Consider versioning for major changes

## Error Handling Patterns

### Response Types
- Use structured error responses
- Provide actionable error messages  
- Include error codes for programmatic handling

### Validation
- Field validation should happen at the API boundary
- Document validation rules in field comments
- Use clear, user-friendly error messages

## Best Practices

### Code Organization
- Group related structs in the same file
- Order structs logically (Post, Put, Get pattern)
- Keep utility functions close to related structs

### Documentation Links
- Reference relevant documentation in comments
- Link to configuration guides where applicable
- Use relative paths for internal documentation

### Examples in Comments
- Provide realistic, practical examples
- Use consistent example data across related structs
- Avoid placeholder text like "foo" unless demonstrating naming

### Naming Consistency
- Follow established patterns within the package
- Use consistent terminology across similar objects
- Prefer explicit names over abbreviated forms

## Common Pitfalls to Avoid

- **Don't** include read-only fields in PUT structs
- **Don't** break existing JSON/YAML serialization
- **Don't** change field types without considering API compatibility
- **Don't** forget to update both Writable() and SetWritable() methods
- **Don't** add fields without proper documentation and examples
- **Avoid** nested structs unless necessary for API clarity
- **Avoid** complex validation logic in struct definitions

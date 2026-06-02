package parser

// languageQueries maps a language constant to its tree-sitter S-expression query string.
var languageQueries = map[string]string{
	LangGo: `
(function_declaration
  name: (identifier) @name
  parameters: (parameter_list) @params
  result: (_)? @result
  body: (block) @body) @definition

(method_declaration
  receiver: (parameter_list) @receiver
  name: (field_identifier) @name
  parameters: (parameter_list) @params
  result: (_)? @result
  body: (block) @body) @definition

(type_declaration
  (type_spec
    name: (type_identifier) @name
    type: (struct_type))) @struct_definition

(type_declaration
  (type_spec
    name: (type_identifier) @name
    type: (interface_type))) @interface_definition
`,

	LangPython: `
(function_definition
  name: (identifier) @name
  parameters: (parameters) @params
  body: (block) @body) @definition

(class_definition
  name: (identifier) @name
  body: (block) @body) @class_definition
`,

	LangTypeScript: `
(function_declaration
  name: (identifier) @name
  parameters: (formal_parameters) @params
  body: (statement_block) @body) @definition

(method_definition
  name: (property_identifier) @name
  parameters: (formal_parameters) @params
  body: (statement_block) @body) @definition

(class_declaration
  name: (type_identifier) @name
  body: (class_body) @body) @class_definition

(interface_declaration
  name: (type_identifier) @name
  body: (interface_body) @body) @interface_definition

(lexical_declaration
  (variable_declarator
    name: (identifier) @name
    value: (arrow_function
      parameters: (_) @params
      body: (_) @body))) @definition
`,

	LangJavaScript: `
(function_declaration
  name: (identifier) @name
  parameters: (formal_parameters) @params
  body: (statement_block) @body) @definition

(method_definition
  name: (property_identifier) @name
  parameters: (formal_parameters) @params
  body: (statement_block) @body) @definition

(class_declaration
  name: (identifier) @name
  body: (class_body) @body) @class_definition

(lexical_declaration
  (variable_declarator
    name: (identifier) @name
    value: (arrow_function
      parameters: (_) @params
      body: (_) @body))) @definition
`,

	LangRust: `
(function_item
  name: (identifier) @name
  parameters: (parameters) @params
  return_type: (_)? @result
  body: (block) @body) @definition

(impl_item
  body: (declaration_list
    (function_item
      name: (identifier) @name
      parameters: (parameters) @params
      body: (block) @body) @definition))

(struct_item
  name: (type_identifier) @name
  body: (_) @body) @struct_definition

(enum_item
  name: (type_identifier) @name
  body: (enum_variant_list) @body) @enum_definition

(trait_item
  name: (type_identifier) @name
  body: (declaration_list) @body) @trait_definition
`,

	LangJava: `
(method_declaration
  name: (identifier) @name
  parameters: (formal_parameters) @params
  body: (block) @body) @definition

(class_declaration
  name: (identifier) @name
  body: (class_body) @body) @class_definition

(interface_declaration
  name: (identifier) @name
  body: (interface_body) @body) @interface_definition
`,
}

// QueryForLanguage returns the tree-sitter query string for the given language constant.
// Returns "" if the language is not supported.
func QueryForLanguage(lang string) string {
	return languageQueries[lang]
}

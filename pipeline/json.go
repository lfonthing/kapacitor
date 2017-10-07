package pipeline

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/influxdata/influxdb/influxql"
	"github.com/influxdata/kapacitor/tick/ast"
)

const (
	// NodeTypeOf is used by all Node to identify the node duration Marshal and Unmarshal
	NodeTypeOf = "typeOf"
	// NodeID is used by all Node as a unique ID duration Marshal and Unmarshal
	NodeID = "id"
)

// TypeOf is a helper struct to add type information for each pipeline node
type TypeOf struct {
	Type string `json:"typeOf"`
	ID   ID     `json:"id,string"`
}

// Marshal will be removed
func Marshal(n Node) ([]byte, error) {
	switch node := n.(type) {
	case *WindowNode:
		type _WindowNode WindowNode
		return json.Marshal(struct {
			*WindowNode
			TypeOf
		}{
			WindowNode: node,
			TypeOf: TypeOf{
				Type: "window",
				ID:   n.ID(),
			},
		})

	}
	return nil, nil
}

// Edge is a connection between a parent ID and a ChildID
type Edge struct {
	Parent string `json:"parent"`
	Child  string `json:"child"`
}

// JSONPipeline is the JSON serialization format for Pipeline
type JSONPipeline struct {
	Nodes    []*JSONNode `json:"nodes"`
	Edges    []Edge      `json:"edges"`
	ids      map[string]*JSONNode
	types    map[string]string
	srcs     map[string]Node
	stats    []string
	sorted   []string
	parents  map[string][]string
	children map[string][]string
}

// Unmarshal will decode JSON structure into a cache of maps
func (j *JSONPipeline) Unmarshal(data []byte, v interface{}) error {
	type _j struct {
		*JSONPipeline
	}
	// Prevent recursion by creating a fake type
	if err := json.Unmarshal(data, _j{j}); err != nil {
		return err
	}
	return nil
	//return j.cache()
}

/*
func (j *JSONPipeline) cache() error {
	for _, node := range j.Nodes {
		typ, err := node.Type()
		if err != nil {
			return err
		}
		id, err := node.ID()
		if err != nil {
			return err
		}

		j.ids[id] = node
		j.types[id] = typ

		if src, ok := node.IsSource(typ); ok {
			j.srcs[id] = src
		}

		if node.IsStat(typ) {
			j.stats = append(j.stats, id)
		}
	}

	j.toGraph()

	sorter := PipelineSorter{
		Graph: j.parents,
	}
	var err error
	j.sorted, err = sorter.Sort()
	return err
}*/

func (j *JSONPipeline) Parents(n string) []string {
	return j.children[n]
}

func (j *JSONPipeline) toGraph() {
	for _, edge := range j.Edges {
		parent, ok := j.parents[edge.Parent]
		if !ok {
			parent = []string{}
		}
		parent = append(parent, edge.Child)
		j.parents[edge.Parent] = parent

		child, ok := j.children[edge.Child]
		if !ok {
			child = []string{}
		}
		child = append(child, edge.Parent)
		j.children[edge.Child] = child
	}
}

// Sources returns all source nodes in the raw JSONPipeline
func (j *JSONPipeline) Sources() map[string]Node {
	return j.srcs
}

func (j *JSONPipeline) Sorted() []string {
	return j.sorted
}

type PipelineSorter struct {
	Graph     map[string][]string
	permanent map[string]bool
	temporary map[string]bool
	sorted    []string
}

func (p *PipelineSorter) Sort() ([]string, error) {
	for n := range p.Graph {
		if err := p.visit(n); err != nil {
			return nil, err
		}
	}
	return p.sorted, nil
}

func (p *PipelineSorter) visit(node string) error {
	if _, marked := p.permanent[node]; marked {
		return nil
	}
	if _, marked := p.temporary[node]; marked {
		return fmt.Errorf("cycle detected. kapacitor pipelines must not have cycles")
	}
	p.temporary[node] = true
	children := p.Graph[node]
	for _, child := range children {
		p.visit(child)
	}
	p.permanent[node] = true
	p.sorted = append([]string{node}, p.sorted...)
	return nil
}

// JSONNode contains all fields associated with a node.  `typeOf`
// is used to determine which type of node this is.
type JSONNode map[string]interface{}

// NewJSONNode decodes JSON bytes into a JSONNode
func NewJSONNode(data []byte) (JSONNode, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var input JSONNode
	err := dec.Decode(&input)
	return input, err
}

// CheckTypeOf tests that the typeOf field is correctly set to typ.
func (j JSONNode) CheckTypeOf(typ string) error {
	t, ok := j[NodeTypeOf]
	if !ok {
		return fmt.Errorf("missing typeOf field")
	}

	if t != typ {
		return fmt.Errorf("error unmarshaling node type %s; received %s", typ, t)
	}
	return nil
}

// SetType adds the Node type information
func (j JSONNode) SetType(typ string) JSONNode {
	j[NodeTypeOf] = typ
	return j
}

// SetID adds the Node ID information
func (j JSONNode) SetID(id ID) JSONNode {
	j[NodeID] = fmt.Sprintf("%d", id)
	return j
}

// Set adds the key/value to the JSONNode
func (j JSONNode) Set(key string, value interface{}) JSONNode {
	j[key] = value
	return j
}

// SetDuration adds key to the JSONNode but formats the duration in InfluxQL style
func (j JSONNode) SetDuration(key string, value time.Duration) JSONNode {
	return j.Set(key, influxql.FormatDuration(value))
}

// Has returns true if field exists
func (j JSONNode) Has(field string) bool {
	_, ok := j[field]
	return ok
}

// Field returns expected field or error if field doesn't exist
func (j JSONNode) Field(field string) (interface{}, error) {
	fld, ok := j[field]
	if !ok {
		return nil, fmt.Errorf("missing expected field %s", field)
	}
	return fld, nil
}

// String reads the field for a string value
func (j JSONNode) String(field string) (string, error) {
	s, err := j.Field(field)
	if err != nil {
		return "", err
	}

	str, ok := s.(string)
	if !ok {
		return "", fmt.Errorf("field %s is not a string value but is %T", field, s)
	}
	return str, nil
}

// Int64 reads the field for a int64 value
func (j JSONNode) Int64(field string) (int64, error) {
	n, err := j.Field(field)
	if err != nil {
		return 0, err
	}

	jnum, ok := n.(json.Number)
	if ok {
		return jnum.Int64()
	}
	num, ok := n.(int64)
	if ok {
		return num, nil
	}
	return 0, fmt.Errorf("field %s is not an integer value but is %T", field, n)
}

// Float64 reads the field for a float64 value
func (j JSONNode) Float64(field string) (float64, error) {
	n, err := j.Field(field)
	if err != nil {
		return 0, err
	}

	jnum, ok := n.(json.Number)
	if ok {
		return jnum.Float64()
	}
	num, ok := n.(float64)
	if ok {
		return num, nil
	}
	return 0, fmt.Errorf("field %s is not a floating point value but is %T", field, n)
}

// Strings reads the field an array of strings
func (j JSONNode) Strings(field string) ([]string, error) {
	s, err := j.Field(field)
	if err != nil {
		return nil, err
	}

	strs, ok := s.([]string)
	if !ok {
		return nil, fmt.Errorf("field %s is not an array of strings but is %T", field, s)
	}
	return strs, nil
}

// Duration reads the field and assumes the string is in InfluxQL Duration format.
func (j JSONNode) Duration(field string) (time.Duration, error) {
	d, err := j.Field(field)
	if err != nil {
		return 0, err
	}

	dur, ok := d.(string)
	if !ok {
		return 0, fmt.Errorf("field %s is not a string duration value but is %T", field, d)
	}

	return influxql.ParseDuration(dur)
}

// Bool reads the field for a boolean value
func (j JSONNode) Bool(field string) (bool, error) {
	b, err := j.Field(field)
	if err != nil {
		return false, err
	}

	boolean, ok := b.(bool)
	if !ok {
		return false, fmt.Errorf("field %s is not a bool value but is %T", field, b)
	}
	return boolean, nil
}

// Lambda reads the field as an ast.LambdaNode
func (j JSONNode) Lambda(field string) (*ast.LambdaNode, error) {
	n, err := j.Field(field)
	if err != nil {
		return nil, err
	}

	if n == nil {
		return nil, nil
	}

	lamb, ok := n.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("field %s is not a lambda expression but is %T", field, n)
	}

	lambda := &ast.LambdaNode{}
	err = lambda.Unmarshal(lamb)
	return lambda, err
}

// Type returns the Node type.  This name can be used
// as the chain function name for the parent node.
func (j JSONNode) Type() (string, error) {
	return j.String(NodeTypeOf)
}

// ID returns the unique ID for this node.  This ID is used
// as the id of the parent and children in the Edges structure.
func (j JSONNode) ID() (ID, error) {
	i, err := j.String(NodeID)
	if err != nil {
		return 0, err
	}
	id, err := strconv.Atoi(i)
	return ID(id), err
}

// IsSource returns the Stream or BatchNode if this is a stream or batch.
func (j JSONNode) IsSource(typ string) (Node, bool) {
	switch typ {
	case "stream":
		return newStreamNode(), true
	case "batch":
		return newBatchNode(), true
	default:
		return nil, false
	}
}

// IsStat returns true if this node is a stat node.
func (j JSONNode) IsStat(typ string) bool {
	if typ == "stats" {
		return true
	}
	return false
}

/*
// Unmarshal deserializes the pipeline from JSON.
func (p *Pipeline) Unmarshal(data []byte, v interface{}) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var input JSONPipeline
	if err := dec.Decode(&input); err != nil {
		return err
	}
	nodes := map[string]Node{}
	sorted := input.Sorted()
	for _, n := range sorted {
		parents := input.Parents(n)
		if len(parents) == 0 {
			return fmt.Errorf("Node ID %s requires at least one parent", n)
		}
		root := nodes[parents[0]]
		node, ok := input.ids[n]
		if !ok {
			return fmt.Errorf("Node ID %s has edge but no node body", n)
		}
		typ, err := node.Type()
		if err != nil {
			return err
		}
		switch typ {
		case "stream":
			strm := newStreamNode()
			nodes[n] = strm
			p.addSource(strm)
		case "batch":
			batch := newBatchNode()
			nodes[n] = batch
			p.addSource(batch)
		case "from":
			strm, ok := root.(*StreamNode)
			if !ok {
				return fmt.Errorf("Node ID %s parent is not a stream but %T", n, root)
			}
			from := strm.From()
			nodes[n] = from
			rFrom, err := tick.NewReflectionDescriber(from, nil)
			if err != nil {
				return err
			}
			for k, v := range *node {
				if k == "typeOf" || k == "id" {
					continue
				}
				rFrom.PropertyType
			}
		}
	}
	return nil
}
*/
/*
func ChainArgs(node *JSONNode) ([]interface{}, error) {
	typ, err := node.Type()
	if err != nil {
		return nil, err
	}
	switch typ {
	case "where":
		node["lambda"]
		return nil, []string{"lambda"}
	case "httpOut":
		return nil, []string{"endpoint"}
	case "httpPost":
		return nil, []string{"urls"}
	case "union":
		// TODO:
	case "join":
		// TODO:
	case "combine":
		return nil, []string{"lambdas"}
	case "eval":
		return nil, []string{"lambdas"}
	case "groupBy":
		return nil, []string{"dimensions"}
	case "sample":
		// TODO:
	case "derivative":
		return nil, []string{"field"}
	case "shift":
		return nil, []string{"shift"}
	case "stateDuration":
		return nil, []string{"lambda"}
	case "stateCount":
		return nil, []string{"lambda"}
	default:
		return nil, []string{}
	}
}

*/
// Copyright 2012 Daniel Connelly.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// A library for efficiently storing and querying spatial data.
package rtreego

import (
	"fmt"
	"math"
)

// Rtree represents an R-tree, a balanced search tree for storing and querying
// spatial objects.  Dim specifies the number of spatial dimensions and
// MinChildren/MaxChildren specify the minimum/maximum branching factors.
type Rtree struct {
	Dim         int
	MinChildren int
	MaxChildren int
	root        node
	size        int
}

// NewTree creates a new R-tree instance.  
func NewTree(Dim, MinChildren, MaxChildren int) *Rtree {
	rt := Rtree{Dim: Dim, MinChildren: MinChildren, MaxChildren: MaxChildren}
	rt.root.entries = []entry{}
	rt.root.leaf = true
	return &rt
}

// Size returns the number of objects currently stored in tree.
func (tree *Rtree) Size() int {
	return tree.size
}

func (tree *Rtree) String() string {
	return "foo"
}

// Depth returns the maximum depth of tree.
func (tree *Rtree) Depth() int {
	var nodeDepth func(n *node) int
	nodeDepth = func(n *node) int {
		if n.leaf {
			return 1
		}
		sum := 0
		for _, e := range n.entries {
			sum += nodeDepth(e.child)
		}
		return sum
	}
	return nodeDepth(&tree.root)
}

// node represents a tree node of an Rtree.
type node struct {
	parent  *node
	leaf    bool
	entries []entry
}

func (n *node) String() string {
	return fmt.Sprintf("node{leaf: %v, entries: %v}", n.leaf, n.entries)
}

// entry represents a spatial index record stored in a tree node.
type entry struct {
	bb    *Rect // bounding-box of all children of this entry
	child *node
	obj   Spatial
}

func (e entry) String() string {
	if e.child != nil {
		return fmt.Sprintf("entry{bb: %v, child: %v}", e.bb, e.child)
	}
	return fmt.Sprintf("entry{bb: %v, obj: %v}", e.bb, e.obj)
}

// Any type that implements Spatial can be stored in an Rtree and queried.
type Spatial interface {
	Bounds() *Rect
}

// Insertion

// Insert inserts a spatial object into the tree.  A DimError is returned if
// the dimensions of the object don't match those of the tree.  If insertion
// causes a leaf node to overflow, the tree is rebalanced automatically.
//
// Implemented per Section 3.2 of "R-trees: A Dynamic Index Structure for
// Spatial Searching" by A. Guttman, Proceedings of ACM SIGMOD, p. 47-57, 1984.
func (tree *Rtree) Insert(obj Spatial) error {
	leaf := tree.chooseLeaf(&tree.root, obj)
	leaf.entries = append(leaf.entries, entry{obj.Bounds(), nil, obj})
	var split *node
	if len(leaf.entries) > tree.MaxChildren {
		leaf, split = leaf.split(tree.MinChildren)
	}
	root, splitRoot := tree.adjustTree(leaf, split)
	if splitRoot != nil {
		oldRoot := *root
		tree.root = node{
			parent: nil,
			entries: []entry{
				entry{bb: oldRoot.computeBoundingBox(), child: &oldRoot},
				entry{bb: splitRoot.computeBoundingBox(), child: splitRoot},
			},
		}
		oldRoot.parent = &tree.root
		splitRoot.parent = &tree.root
	}
	tree.size++
	return nil
}

// chooseLeaf finds the leaf node in which obj should be inserted.
func (tree *Rtree) chooseLeaf(n *node, obj Spatial) *node {
	if n.leaf {
		return n
	}

	// find the entry whose bb needs least enlargement to include obj
	diff := math.MaxFloat64
	var chosen entry
	for _, e := range n.entries {
		bb := boundingBox(e.bb, obj.Bounds())
		d := bb.size() - e.bb.size()
		if d < diff || (d == diff && e.bb.size() < chosen.bb.size()) {
			diff = d
			chosen = e
		}
	}

	return tree.chooseLeaf(chosen.child, obj)
}

// adjustTree splits overflowing nodes and propagates the changes upwards.
func (tree *Rtree) adjustTree(n, nn *node) (*node, *node) {
	// Let the caller handle root adjustments.
	if n == &tree.root {
		return n, nn
	}

	// Re-size the bounding box of n to account for lower-level changes.
	n.getEntry().bb = n.computeBoundingBox()

	// If nn is nil, then we're just propagating changes upwards.
	if nn == nil {
		return tree.adjustTree(n.parent, nil)
	}

	// Otherwise, these are two nodes resulting from a split.
	// n was reused as the "left" node, but we need to add nn to n.parent.
	enn := entry{nn.computeBoundingBox(), nn, nil}
	n.parent.entries = append(n.parent.entries, enn)

	// If the new entry overflows the parent, split the parent and propagate.
	if len(n.parent.entries) > tree.MaxChildren {
		return tree.adjustTree(n.parent.split(tree.MinChildren))
	}

	// Otherwise keep propagating changes upwards.
	return tree.adjustTree(n.parent, nil)
}

// getEntry returns a pointer to the entry for the node n from n's parent.
func (n *node) getEntry() *entry {
	var e *entry
	for i := range n.parent.entries {
		if n.parent.entries[i].child == n {
			e = &n.parent.entries[i]
			break
		}
	}
	return e
}

// computeBoundingBox finds the MBR of the children of n.
func (n *node) computeBoundingBox() *Rect {
	childBoxes := []*Rect{}
	for _, e := range n.entries {
		childBoxes = append(childBoxes, e.bb)
	}
	return boundingBoxN(childBoxes...)
}

// split splits a node into two groups while attempting to minimize the
// bounding-box area of the resulting groups.
func (n *node) split(minGroupSize int) (left, right *node) {
	// find the initial split
	l, r := n.pickSeeds()
	leftSeed, rightSeed := n.entries[l], n.entries[r]

	// get the entries to be divided between left and right
	remaining := append(n.entries[:l], n.entries[l+1:r]...)
	remaining = append(remaining, n.entries[r+1:]...)

	// setup the new split nodes, but re-use n as the left node
	left = n
	left.entries = []entry{leftSeed}
	right = &node{n.parent, n.leaf, []entry{rightSeed}}

	// distribute all of n's old entries into left and right.
	for len(remaining) > 0 {
		next := pickNext(left, right, remaining)
		e := remaining[next]

		if len(remaining)+len(left.entries) <= minGroupSize {
			assign(e, left)
		} else if len(remaining)+len(right.entries) <= minGroupSize {
			assign(e, right)
		} else {
			assignGroup(e, left, right)
		}

		remaining = append(remaining[:next], remaining[next+1:]...)
	}

	return
}

func assign(e entry, group *node) {
	group.entries = append(group.entries, e)
}

// assignGroup chooses one of two groups to which a node should be added.
func assignGroup(e entry, left, right *node) {
	leftBB := left.computeBoundingBox()
	rightBB := right.computeBoundingBox()
	leftEnlarged := boundingBox(leftBB, e.bb)
	rightEnlarged := boundingBox(rightBB, e.bb)

	// first, choose the group that needs the least enlargement
	leftDiff := leftEnlarged.size() - leftBB.size()
	rightDiff := rightEnlarged.size() - rightBB.size()
	if diff := leftDiff - rightDiff; diff < 0 {
		assign(e, left)
		return
	} else if diff > 0 {
		assign(e, right)
		return
	}

	// next, choose the group that has smaller area
	if diff := leftBB.size() - rightBB.size(); diff < 0 {
		assign(e, left)
		return
	} else if diff > 0 {
		assign(e, right)
		return
	}

	// next, choose the group with fewer entries
	if diff := len(left.entries) - len(right.entries); diff <= 0 {
		assign(e, left)
		return
	}
	assign(e, right)
}

// pickSeeds chooses two child entries of n to start a split.
func (n *node) pickSeeds() (left, right int) {
	maxWastedSpace := -1.0
	for i, e1 := range n.entries {
		for j, e2 := range n.entries[i+1:] {
			d := boundingBox(e1.bb, e2.bb).size() - e1.bb.size() - e2.bb.size()
			if d > maxWastedSpace {
				maxWastedSpace = d
				left, right = i, j+i+1
			}
		}
	}
	return
}

// pickNext chooses an entry to be added to an entry group.
func pickNext(left, right *node, entries []entry) (next int) {
	maxDiff := -1.0
	leftBB := left.computeBoundingBox()
	rightBB := right.computeBoundingBox()
	for i, e := range entries {
		d1 := boundingBox(leftBB, e.bb).size() - leftBB.size()
		d2 := boundingBox(rightBB, e.bb).size() - rightBB.size()
		d := math.Abs(d1 - d2)
		if d > maxDiff {
			maxDiff = d
			next = i
		}
	}
	return
}

// Deletion

// Delete removes an object from the tree.  If the object is not found, ok
// is false; otherwise ok is true.  A DimError is returned if the specified
// object has improper dimensions for the tree.
//
// Implemented per Section 3.3 of "R-trees: A Dynamic Index Structure for
// Spatial Searching" by A. Guttman, Proceedings of ACM SIGMOD, p. 47-57, 1984.
func (tree *Rtree) Delete(obj Spatial) (ok bool, err error) {
	return false, nil
}

// findLeaf finds the leaf node containing obj.
func (tree *Rtree) findLeaf(n *node, obj Spatial) *node {
	if n.leaf {
		return n
	}
	for _, e := range n.entries {
		if e.bb.containsRect(obj.Bounds()) {
			return tree.findLeaf(e.child, obj)
		}
	}
	return nil
}

// condenseTree deletes underflowing nodes and propagates the changes upwards.
func (tree *Rtree) condenseTree(n *node) *node {
	return nil
}

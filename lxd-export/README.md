# Import / export tool


## How to talk to LXD server inside `micro1` on remote laptop?

```bash
ssh -L 8444:10.237.134.244:8443 infinity@192.168.1.29
```

## Design

* EXPORT: Generate a DAG of the source cluster state. Serialize it into a stable JSON (always same ordering if the resources are the same). There are a couple of interesting tricks in the representation of a DAG node. For example here is an example.

```json
{
    id: 89276387648768,
    hid: "storage_pool_default",
    rank: 1,
    data: { <api.StoragePool> }
}
```

* `id` : This is meant to be an 'internal id' as these are big int64 numbers supposed to uniquely represent an entity. This `id` is used by gonum for a lot of base operation. In our case, this id serves an additional purpose: it is as FNV hash of the `data` field. Having such a representation allows to immediately tell if two nodes (of the same type) have a 'diff'. This will be extremely useful in the import phase.

* `hid` : This is a human-readable id. This is used to have a more meaningful representation of the cluster in the JSON file, but is also used to easily query nodes in a graph (when the DAG is generated, a `map[string]int64` mapping is also given (humanIDtoGraphID), so that we can easily reference a gonum node using a human string)

* `rank`: A rank is a positive integer, detailing the 'level' of a node in a DAG. Nodes with the same rank, can be processed (removed / updated) in parallel without danger. They are insured to not be inter-dependent. This is going to be extremely helpful during the 'plan' execution in the import phase: edits with the same rank will be batched and ordered. Then each batch is executed sequentially but the edits inside can run in parallel.

* `data`: This is the inner content of a node (it is always an existing `api.<Entity>` object, except for the root node, which is a combination of two `api.<Entity>`)

___

* IMPORT: Import the JSON file on the DR site. Rebuild the DAG from the source cluster (this is done through our `UnmarshalJSON` function). Build the DAG of the target cluster (the one on which we call the import). The problem is then to find the 'Graph Edit Distance' (optimal sequence of edit operations (also called 'plan') so that the target DAG equals the source DAG)




Note on the IMPORT:

```
The reconciliation steps should indeed follow a specific order, but it's not always strictly the reverse topological order. Let's break this down:

* Adding new nodes: This should follow the topological order of the source DAG.

* Updating existing nodes: This can also follow the topological order of the source DAG.

* Removing nodes: This should follow the reverse topological order of the target DAG.

The reason for this is:

Adding nodes: We need to ensure that when we add a new node, all its dependencies are already in place. The topological order guarantees this.


Updating nodes: Similar to adding, we want to ensure that when we update a node, its dependencies have already been updated or added.


Removing nodes: We want to remove nodes only after all nodes that depend on them have been removed. The reverse topological order ensures this.
```


# Implementation steps:

1) [DONE] Create the DAG builder function. This step will allow us to have a deep understanding of the dependencies between the entities.

2) [DONE] Build the serial. / deserial. logic to store / retrieve a DAG.

3) [WIP] topological and reverse topological traversal (with custom rules) of the imported source DAG with comparison with the target DAG to find the sequence of edits operations ("diffs") so that target reconciles to source. This part is really the heart of this whole import / export problem. But having a handy cluster representation like gonum's `*simple.DirectedGraph` allows to apply smart algos to resolve things... Regarding the "diffs", there are of four types: `ADDED`, `REMOVED`, `UPDATED`, `RENAMED` (In order for us to find the `RENAMED` diffs, we'll need to extend the concept of `volatile.uuid` key to the 'renamable' entities). This sequence of diffs, also called "plan" (Terraform uses the same terminology) is returned to the user for approval.

4) [TODO] Execute the plan. Show the pending edits (grouped by edit batches (edits with the same rank)) being resolved to CLI. We might need to introduce the concept of 'macro' LXD operation to group all these operations.

5) [TODO] Add parallelism in the execution of the plan (there are potentially a LOT of operations (API calls) here). We already built a DAG with the concept of 'rank' (i.e, nodes in a DAG with the same 'rank' can be processed in parallel), so once we build the 'plan', this should not be too hard (hence the advantage of storing our DAG with the ranks)

## Improvement ideas

In the **EXPORT** phase:

- Add option for converting to graphviz: https://github.com/gonum/gonum/blob/master/graph/encoding/dot/encode_test.go
- SVG render of DOT file: https://github.com/goccy/go-graphviz
- Then we can generate an interactive HTML file like:
```html
<!DOCTYPE html>
<html>
<head>
    <title>Clickable LXD cluster graph visualization</title>
    <style>
        #graph { width: 50%; float: left; }
        #iframe-container { width: 50%; float: right; }
        iframe { width: 100%; height: 500px; border: none; }
    </style>
</head>
<body>
    <div id="graph">
        <!-- Generated SVG here -->
        <object id="graphSvg" type="image/svg+xml" data="output.svg"></object>
    </div>
    <div id="iframe-container">
        <iframe id="content-frame"></iframe>
    </div>

    <script>
        document.getElementById('graphSvg').addEventListener('load', function() {
            var svgDoc = this.contentDocument;
            var links = svgDoc.getElementsByTagName('a');

            for (var i = 0; i < links.length; i++) {
                links[i].addEventListener('click', function(event) {
                    event.preventDefault();
                    var url = this.getAttribute('xlink:href');
                    // Remove the '#' from the URL
                    url = url.substring(1);
                    // Set the iframe src to the corresponding page
                    document.getElementById('content-frame').src = url + '.html';
                });
            }
        });
    </script>
</body>
</html>
```

In this simple HTML document, each SVG node is clickable and loads the API resources in an `iframe` (given we added the certificate in the browser). Such a tool would help understand the intricate dependencies between various entities in a LXD cluster. Note: Terraform has a tool like this to generate a DOT encoding of their dependency graph.
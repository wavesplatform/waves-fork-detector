**Waves Fork Detector** is a service that registers forks in the Waves network.

## How it works

To register forks, Fork Detector connects to all available nodes and also allows other nodes to connect to it. The service receives network messages about key-blocks and micro-blocks and builds a graph of all acquired blocks and their forks.

The service stores brief information about blocks, which includes:
* Block ID
* Parent Block ID
* Generator's Address
* Block Timestamp
* Block's Base Target

For every chain, the `Score` value is calculated. By comparing the scores of different chains, we can determine the main chain. The score of a chain depends on the cumulative balance of its generators. That's why the chain created by generators with a greater cumulative balance will have a higher `Score` value and will be the main chain. The main chain is marked by the `true` value in the `longest` field.

For every fork, the pointer to the latest block of the fork, called `head`, is stored. For any two different chains, it is possible to find the last common block (field `last_common_block`). For the main chain, the value of `last_common_block` is always the same as `head_block`, and the length of the fork (field `length`) is always 0.

For any alternative fork different from the main chain (forks are always compared to the main chain), the last common block is the block after which the chains diverged, and the length of the fork is equal to the number of blocks generated on that fork.

If the alternative chain has the same block ID in the `head_block` and `last_common_block` fields, and the `length` is zero, that means the node on the fork is stalled and doesn’t generate or accept any blocks.

For every node that has ever connected, there is a link to the last block received from that node, called `leash`. When receiving updates from a node, Fork Detector updates the `leash` for that node. For disconnected nodes, the `leash` points to the last received block.

### Block graph initialization

On the first run, Fork Detector builds the graph of blocks starting from the Genesis block in the same way as a node does. During the initial graph build, the number of alternative chains is minimal.

### Supported IP address versions

Fork Detector supports only IPv4 addresses. This limitation exists due to the Waves network protocol, which exclusively uses IPv4 addresses when exchanging information about nodes.

### Version selection

One must provide a list of protocol versions supported by Fork Detector. Connecting to nodes with different versions allows the detection of alternative chains that may arise due to lag or the inability of some generators to update during a Waves network upgrade.
When establishing an outgoing connection to a new node, Fork Detector attempts to handshake with each version from the list until a successful connection is made. For incoming connections from unknown nodes, Fork Detector responds with the version received from the node if that version is included in the list.

## HTTP API Methods

### `GET` `/api/peers/all`

The method returns a list of addresses of all known nodes. These addresses were received from other nodes during the known node exchange, but a connection to all of them is not guaranteed.

Reply example:
```json
[
  {
    "address": "79.199.35.153:0",
    "nonce": 0,
    "name": "",
    "version": "0.0.0",
    "state": 0,
    "next_attempt": "0001-01-01T00:00:00Z",
    "score": null
  }
]
```

Reply fields:
* `address` - IP address and port of the node. The port has a zero value in case of an incoming connection (node on an ephemeral port) or if no connection was established.
* `nonce` - random nonce; zero if there was no connection to the node.
* `name` - name of the node; empty string if no connection was made.
* `version` - node’s version; zero if no connection was established.
* `state` - state of the connection to the node:
    * `0` `Unknown`- unknown node, no successful connection was made to the address.
    * `1` `Connected` - successful connection was made, and the network byte and version are acceptable.
    * `2` `Hostile` - successful connection was established, but the network byte or version is not acceptable.
* `next_attempt` - time when the next attempt to connect to the node is planned. This field is used during version selection (see the section [Version Selection](#version-selection)).
* `score` - last known value of the Score; `null` value if there was no connection.

### `GET` `/api/peers/friendly`

The method returns a list of node addresses to which a successful connection was established (state has a value of 1). The list may include addresses where a successful connection was made in the past but is no longer possible.

Reply example:
```json
[
  {
    "address": "43.156.30.21:0",
    "nonce": 547183,
    "name": "-my-testnet-node",
    "version": "1.5.8",
    "state": 1,
    "next_attempt": "2024-11-06T12:36:11Z",
    "score": 967128354984173501468049
  }
]
```

Reply fields are the same as those for the method [GET /api/peers/all](#get-apipeersall).

### `GET` `/api/connections`

The method returns a list of addresses of nodes that are connected at the time of the request.

Reply example:
```json
[
  {
    "address": "43.156.30.21:0",
    "nonce": 547183,
    "name": "-my-testnet-node",
    "version": "1.5.8",
    "state": 1,
    "next_attempt": "2024-11-06T12:36:11Z",
    "score": 967129143986158086525130
  }
]
```

Reply fields are the same as those for the method [GET /api/peers/all](#get-apipeersall).

### `GET` `/api/heads/`

A service method that returns the list of `heads` of all chains.

Reply example:
```json
[
  {
    "number": 59,
    "id": "4Seud85Bghk1pJxrK4wyW92zW2xTkCuaVpv9PNxnAyp4",
    "height": 4175639,
    "score": 946311268021761697586482,
    "timestamp": "2022-11-12T11:44:01.043Z"
  }
]
```

Reply fields:
* `number` — sequential number of a head.
* `id` — last block ID of the chain that the `head` is pointing to.
* `height` — height of the last block in the chain (number of blocks in the chain).
* `score` — calculated value of the `Score` for the chain.
* `timestamp` — timestamp of the last block.

### `GET` `/api/leashes`

A service method that returns the list of `leashes` that link nodes to their last blocks.

Reply example:
```json
[
  {
    "block_id": "ApMd6i85Xmy2ppLADRwkMm7oDmrU2gswtcWwkDNdKnjt",
    "height": 4427078,
    "score": 967109273317885729102590,
    "timestamp": "2024-11-07T13:12:43.06Z",
    "generator": "3PQ9hZ36dyXGcqabcrHXsjP9PaQMqy69yeE",
    "peers_count": 1,
    "peers": [
      "52.203.105.100"
    ]
  }
]
```

Reply fields:
* `block_id` — block ID.
* `height` — block height.
* `score` — calculated `Score` value for the chain up to the `block_id` block.
* `timestamp` — block creation timestamp.
* `generator` — block generator’s address.
* `peers_count` — number of nodes that report the  block.
* `peers` — list of addresses of nodes that report the block.

### `GET` `/api/status`

The method provides brief information about the Fork Detector service.

Reply example:
```json
{
  "version": "v0.3.3-4-g475be2f",
  "short_forks_count": 371,
  "long_forks_count": 76,
  "all_peers_count": 1993,
  "friendly_peers_count": 737,
  "connected_peers_count": 156,
  "goroutines_count": 579
}
```

Reply fields:
* `version` — the version of Fork Detector.
* `short_forks_count` — the number of alternative chains with a length of fewer than 5 blocks.
* `long_forks_count` — the number of alternative chains with a length of more or equal than 5 blocks.
* `all_peers_count` — the number of known nodes.
* `friendly_peers_count` — the number of successfully connected nodes.
* `connected_peers_count` — the number of ongoing connections.
* `goroutines_count` — the number of goroutines.

### `GET` `/api/forks`

The method returns the list of all chains. A chain is returned if at least one node is pointing to it. The list may contain chains that are no longer active.

Request parameters:
* `peers` — a comma-separated list of node addresses. The reply will contain only information for the listed nodes. If the list is empty or the parameter is missing, all chains will be returned.

Request example:
```bash
curl -X GET "http://127.0.0.1:8080/api/forks?peers=78.46.193.104,78.46.163.61,135.181.47.20,88.99.139.9"
```

Reply example:
```json
[
  {
    "longest": false,
    "head_block": "6i1F61esPX8Gbyhs14Df39e1ewS5CnnLJVkehjg7D4aA",
    "head_timestamp": "2024-11-07T17:48:58.065Z",
    "head_generator": "3PA1KvFfq9VuJjg45p2ytGgaNjrgnLSgf4r",
    "head_height": 4427350,
    "score": 967130510550401075525343,
    "peers_count": 1,
    "peers": [
      "209.145.51.189"
    ],
    "last_common_block": "6i1F61esPX8Gbyhs14Df39e1ewS5CnnLJVkehjg7D4aA",
    "length": 0
  }
]
```

Reply fields:
* `longest` — indicates the longest chain, with a value of true for the main chain.
* `head_block` — ID of the last block in the chain.
* `head_timestamp` — timestamp of the last block creation.
* `head_generator` — address of the generator of the last block.
* `head_height` — height of the last block in the chain.
* `score` — calculated `Score` value for the chain.
* `peers_count` — number of nodes on the chain.
* `peers` — tlist of addresses of the nodes on the chain.
* `last_common_block` — ID of the last common block with the main chain.
* `length` — length of the chain, i.e., the number of blocks on the alternative chain since it diverged from the main chain.

### `GET` `/api/active-forks`

The method returns a list of chains linked to the currently connected nodes.

Request parameters:
* `peers` — a comma-separated list of node addresses. The reply will contain only information for the listed nodes. If the list is empty or the parameter is missing, all chains will be returned.

Request example:
```bash
curl -X GET "http://127.0.0.1:8080/api/active-forks?peers=78.46.193.104,78.46.163.61,135.181.47.20,88.99.139.9"
```

Reply example:
```json
[
  {
    "longest": false,
    "head_block": "4Seud85Bghk1pJxrK4wyW92zW2xTkCuaVpv9PNxnAyp4",
    "head_timestamp": "2022-11-12T11:44:01.043Z",
    "head_generator": "3P61vXQdD3ykzwcVWabWjGAN4Vp9Go2macT",
    "head_height": 4175639,
    "score": 946311268021761697586482,
    "peers_count": 1,
    "peers": [
      "165.227.41.193"
    ],
    "last_common_block": "DhGK9NAM1ud9Jts1mabkfHDZXpNKhPbJV5ChTRStb2kW",
    "length": 96
  }
]
```

Reply fields are the same as those for the method [GET /api/forks](#get-apiforks).

### `GET` `/api/fork/{address}`

The method returns information about the chain linked to the given node. This method is useful for quickly checking the node’s fork.

Request example:
```bash
curl -X GET "http://127.0.0.1:8080/api/fork/78.46.193.104"
```

Reply example:
```json
{
  "longest": true,
  "head_block": "yaUGcPLA5PCocexUCsigWSrnSFUMJxoGfias2yKM5J5",
  "head_timestamp": "2024-11-07T18:15:58.837Z",
  "head_generator": "3PLp1QsFxukK5nnTBYHAqjz9duWMriDkHeT",
  "head_height": 4427381,
  "score": 967132832313917783152756,
  "peers_count": 115,
  "peers": [
    "3.75.7.51",
	...
    "217.168.76.86"
  ],
  "last_common_block": "yaUGcPLA5PCocexUCsigWSrnSFUMJxoGfias2yKM5J5",
  "length": 0
}
```

Reply fields are the same as those for the method [GET /api/forks](#get-apiforks).

### `GET` `/api/forks/{address}/generators/{blocks}`

The method returns an aggregated list of generator addresses for the chain of the given node and for the specified number of the most recent blocks.

Request example:
```bash
curl -X GET "http://127.0.0.1:8080/api/fork/45.33.22.96/generators/10"
````

Reply example:
```json
[
  {
    "generator": "3PQ9hZ36dyXGcqabcrHXsjP9PaQMqy69yeE",
    "blocks": 4
  },
  {
    "generator": "3PFFEtf7YDCvB3CWPSVGZLFa5Y8PM1KjW5U",
    "blocks": 1
  },
  {
    "generator": "3P8RiVSaQU8ehGLJ5QQNQzyxqYWvWbQb7iC",
    "blocks": 1
  },
  {
    "generator": "3PFcMotvQA8vxzA9NFKFBz6AY7GXD1AgXKP",
    "blocks": 1
  },
  {
    "generator": "3PLp1QsFxukK5nnTBYHAqjz9duWMriDkHeT",
    "blocks": 1
  },
  {
    "generator": "3PGobRuQzBY9VbeKLaZqrcQtW26wrE9jFm7",
    "blocks": 1
  },
  {
    "generator": "3P9DEDP5VbyXQyKtXDUt2crRPn5B7gs6ujc",
    "blocks": 1
  }
]
```

Reply fields:
* `generator` — address of the block generator.
* `blocks` — number of blocks created by the generator in the last `{blocks}` blocks.

## Running Fork Detector

To start Fork Detector with minimal configuration, run the following command:

```bash
forkdetector -db [path_to_db] -peers [IP:PORT,...,IP:PORT] 
```

This command will start Fork Detector to work with the `MainNet` network in outgoing-only connection mode, with the local API available on port `8080`.

Available command-line parameters:
* `-log-level` - logging level, default is `INFO`. Other possible values: `DEBUG`, `INFO`, `WARN`, `ERROR`, and `FATAL`.
* `-blockchain-type` - network type, available values are `mainnet`, `testnet`, and `stagenet`. Default is `mainnet`.
* `-peers` - a comma-separated list of node addresses to initially connect to in the format `ip:port,...,ip:port`. This parameter is required.
* `-api` - the address at which the Fork Detector API will be available. The default is `localhost:8080`, meaning the API will be available locally on port `8080`.
* `-net` - the address at which the network interface for node connections will be available. The default is `localhost:6868`, meaning only local connections are allowed. To allow external connections, use `0.0.0.0:6868`.
* `-declared-address` - the address declared by Fork Detector for node connections. By default, this value is not set, and external connections are not available. The specified address must be a public IP of the server, and the port must match the value in the `-net` parameter.
* `-name` - he name of the node declared by Fork Detector. Default is `forkdetector`.
* `-versions` - the list of supported node versions for connection. The default is `1.5`. The format is `Major.Minor`, separated by commas (e.g., `1.5,1.4,1.3`).

## Building Fork Detector

Go version 1.23 must be installed to build the Fork Detector.

To build the project, run the following command:
```bash
make dist
```

The build result will be located in the `build/dist `directory as an archive with a name in the format `forkdetector_v{X.Y.Z}_{OS}_{ARCH}.[tar.gz|zip]`.
For now, the following operating systems are supported: `Linux`, `macOS`, and `Windows`. The `amd64` and `arm64` architectures are supported for `Linux` and `macOS`, while only the `amd64` architecture is supported for `Windows`.
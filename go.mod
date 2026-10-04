module github.com/timurcravtov/demo-host-server

go 1.25.1

require github.com/timurcravtov/walrus v0.0.0

// Local development: the engine sits next to this folder. Remove for a released version.
replace github.com/timurcravtov/walrus => ../walrus

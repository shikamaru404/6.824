package kvraft

import (
	"../labrpc"
	"crypto/rand"
	"math/big"
	"time"
)

type Clerk struct {
	servers []*labrpc.ClientEnd
	// You will have to modify this struct.
	id				int64
	seq				int
	leadCache int
}

func nrand() int64 {
	max := big.NewInt(int64(1) << 62)
	bigx, _ := rand.Int(rand.Reader, max)
	x := bigx.Int64()
	return x
}

func MakeClerk(servers []*labrpc.ClientEnd) *Clerk {
	ck := new(Clerk)
	ck.servers = servers
	// You'll have to add code here.
	// gen client id, not guaranteed to be global unique
	ck.id = nrand() + time.Now().Unix()
	ck.seq = 0
	DPrintf("[debug cli] new clerk id %d\n", ck.id)
	return ck
}

//
// fetch the current value for a key.
// returns "" if the key does not exist.
// keeps trying forever in the face of all other errors.
//
// you can send an RPC with code like this:
// ok := ck.servers[i].Call("KVServer.Get", &args, &reply)
//
// the types of args and reply (including whether they are pointers)
// must match the declared types of the RPC handler function's
// arguments. and reply must be passed as a pointer.
//
func (ck *Clerk) Get(key string) string {
	// You will have to modify this function.
	args := &GetArgs{Key: key}
	reply := &GetReply{}
	DPrintf("[debug app] get %s\n", key)
	for ; ; ck.leadCache = (ck.leadCache + 1) % len(ck.servers) {
	  ok := ck.servers[ck.leadCache].Call("KVServer.Get", args, reply)
	  if ok {
			switch reply.Err {
			case NotLead:
			case Fail:
				return ""
			case NoErr:
				return reply.Value
			}
		}
	}
}

//
// shared by Put and Append.
//
// you can send an RPC with code like this:
// ok := ck.servers[i].Call("KVServer.PutAppend", &args, &reply)
//
// the types of args and reply (including whether they are pointers)
// must match the declared types of the RPC handler function's
// arguments. and reply must be passed as a pointer.
//
func (ck *Clerk) PutAppend(key string, value string, op string) {
	// You will have to modify this function.
	args := &PutAppendArgs{Key: key, Value: value, Op: op}
	args.ClientID = ck.id
	args.Seq = ck.seq
	ck.seq++
	reply := &PutAppendReply{}
	for ; ; ck.leadCache = (ck.leadCache + 1) % len(ck.servers) {
		ok := ck.sendPutAppendAndWait(ck.leadCache, args, reply)
		if ok {
			return
		}
	}
}

// sendPutAppendAndWait would init a goroutine to send PutAppend rpc
// and wait for the result within timeout.
func (ck *Clerk) sendPutAppendAndWait(to int, args *PutAppendArgs, reply *PutAppendReply) bool {
	ch := make(chan PutAppendReply, 1)

	// goroutine for sending PutAppend rpc
	// if client timeout, this goroutine would recv ctx.Done()
	// and close the res channel
	go func() {
		if ok := ck.servers[to].Call("KVServer.PutAppend", args, reply); ok {
		  ch <- *reply
		}
	}()

	select {
	case reply := <-ch:
		DPrintf("[debug app] reply from %d err %s\n", to, reply.Err)
		return reply.Err == NoErr
	case <-time.After(time.Second):
		DPrintf("[debug app] request to %d timeout\n", to)
		return false
	}
}

func (ck *Clerk) Put(key string, value string) {
	DPrintf("[debug app] Clerk put key %s value %s\n", key, value)
	ck.PutAppend(key, value, "Put")
}
func (ck *Clerk) Append(key string, value string) {
	DPrintf("[debug app] Clerk append key %s value %s\n", key, value)
	ck.PutAppend(key, value, "Append")
}

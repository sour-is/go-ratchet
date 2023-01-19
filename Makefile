ALICE=alice@sour.is
ALICE_KEY=alice.key

BOB=bob@sour.is
BOB_KEY=bob.key

run:
	@rm -rf ./tmp
	@echo Alice starts by offering Bob to upgrade the connection.
	@echo
	@go run ./cmd/ratchet offer --me $(ALICE) --them $(BOB) --key $(ALICE_KEY) --state ./tmp | tee offer.msg

	@echo
	@echo "Bob acknowledges Alice's offer."
	@echo
	@cat offer.msg | go run ./cmd/ratchet recv --me $(BOB) --key $(BOB_KEY) --state ./tmp | tee ack.msg

	@echo
	@echo "Alice evaluates Bob's acknowledgement."
	@echo
	@cat ack.msg | go run ./cmd/ratchet recv --me $(ALICE) --key $(ALICE_KEY)  --state ./tmp 

	@echo
	@echo Alice sends message
	@echo
	@echo hello | go run ./cmd/ratchet send --me $(ALICE) --them $(BOB) --key $(ALICE_KEY)  --state ./tmp | tee send1.msg

	@echo
	@echo Bob receives message. sends reply
	@echo
	@cat send1.msg | go run ./cmd/ratchet recv --me $(BOB) --key $(BOB_KEY) --state ./tmp 
	@echo yoyo | go run ./cmd/ratchet send --me $(BOB) --them $(ALICE) --key $(BOB_KEY)  --state ./tmp | tee send2.msg

	@echo
	@echo Alice receives message. sends close
	@echo
	@cat send2.msg | go run ./cmd/ratchet recv --me $(ALICE) --key $(ALICE_KEY)  --state ./tmp 	
	@go run ./cmd/ratchet close --me $(ALICE) --them $(BOB) --key $(ALICE_KEY)  --state ./tmp | tee close.msg

	@echo
	@echo Bob receives close.
	@echo
	@cat close.msg | go run ./cmd/ratchet recv --me $(BOB)  --them $(ALICE) --key $(BOB_KEY) --state ./tmp 

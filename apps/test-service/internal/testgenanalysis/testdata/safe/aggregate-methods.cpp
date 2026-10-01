struct Pair { int left; int right; };
class Calculator {
public:
    int sum(Pair value) const { return value.left + value.right; }
};
